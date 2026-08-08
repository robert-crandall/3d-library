// Package gcode reads the print settings a slicer left in a G-code file's
// comments.
//
// A sliced file is mostly extrusion moves with a block of `; key = value`
// comments at each end, so the whole package is a scan of two small windows -
// the first 16 KB and the last 128 KB - and a lookup table from the several
// spellings the slicers use to the fields the model page shows. The windows are
// the design, not an optimization: G-code files run to hundreds of megabytes
// and nothing here may grow with file size.
//
// Everything is best-effort. A file this package cannot read is still a file,
// and Parse says so by returning false rather than an error.
package gcode

import (
	"bytes"
	"io"
	"math"
	"strconv"
	"strings"
)

// The two windows, straight from the product brief.
const (
	headBytes = 16 << 10
	tailBytes = 128 << 10
)

// maxCount bounds every value that becomes an int.
//
// Converting an out-of-range float64 to int is *undefined* in Go - the spec
// says the result is implementation-dependent, and on amd64 it is the most
// negative int64. `; perimeters = 999999999999999999999999999999999` parses to
// 1e33 quite legitimately, and without this the panel would render
// "-9223372036854775808 walls". Int32 is the bound because it is the largest
// one that is certainly representable, not because a print has two billion of
// anything: 2^31 seconds is 68 years, and nothing here is a real print long
// before that.
const maxCount = math.MaxInt32

// Meta is what a slicer said about a print.
//
// The numbers are pointers because zero is a real value that has to be told
// apart from "the slicer did not write this". A vase has top_solid_layers = 0,
// a printer without a heated bed has bed_temperature = 0, and either one
// rendered as a blank row would be a lie about the file.
//
// Units are in the names. Lengths stay in the millimetres the slicers write;
// turning them into the metres the panel displays is the frontend's job, so
// nothing here bakes a presentation choice into the database.
type Meta struct {
	Slicer             string   `json:"slicer,omitempty"`
	SlicerVersion      string   `json:"slicerVersion,omitempty"`
	LayerHeightMm      *float64 `json:"layerHeightMm,omitempty"`
	InfillPercent      *float64 `json:"infillPercent,omitempty"`
	InfillPattern      string   `json:"infillPattern,omitempty"`
	WallLoops          *int     `json:"wallLoops,omitempty"`
	TopLayers          *int     `json:"topLayers,omitempty"`
	BottomLayers       *int     `json:"bottomLayers,omitempty"`
	NozzleTempC        *float64 `json:"nozzleTempC,omitempty"`
	BedTempC           *float64 `json:"bedTempC,omitempty"`
	PrintTimeSeconds   *int     `json:"printTimeSeconds,omitempty"`
	FilamentGrams      *float64 `json:"filamentGrams,omitempty"`
	FilamentMm         *float64 `json:"filamentMm,omitempty"`
	FilamentType       string   `json:"filamentType,omitempty"`
	FilamentCost       *float64 `json:"filamentCost,omitempty"`
	MaxVolumetricSpeed *float64 `json:"maxVolumetricSpeed,omitempty"`
	PrinterModel       string   `json:"printerModel,omitempty"`
	Supports           *bool    `json:"supports,omitempty"`
}

// Parse reads the two windows and returns what it learned.
//
// The bool is false when the file taught us nothing worth storing: no
// recognised slicer, or a recognised slicer and not one setting. Both are the
// "no slice settings panel, and no error either" case.
func Parse(r io.ReaderAt, size int64) (Meta, bool) {
	if size <= 0 {
		return Meta{}, false
	}

	var p parser
	p.seen = make(map[string]string, len(candidates))

	// Below the combined window size the two windows would overlap, so the file
	// is read once instead. Above it, these two ReadAts are the only reads this
	// package ever makes, whether the file is 1 MB or 1 GB.
	if size <= headBytes+tailBytes {
		p.scan(window(r, 0, size), true, true)
	} else {
		p.scan(window(r, 0, headBytes), true, false)
		p.scan(window(r, size-tailBytes, tailBytes), false, true)
	}

	m := p.meta()
	return m, m.Slicer != "" && m.fields() > 0
}

// window reads one range. A read error yields no bytes rather than an error: a
// window we could not read is a window with nothing in it, and the other one
// may still have everything.
func window(r io.ReaderAt, off, n int64) []byte {
	buf := make([]byte, n)
	read, err := io.ReadFull(io.NewSectionReader(r, off, n), buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil
	}
	return buf[:read]
}

type parser struct {
	seen    map[string]string
	slicer  string
	version string
}

// scan walks one window's lines. atStart and atEnd say whether the window
// reaches that end of the file; when it does not, the line at that edge was cut
// mid-line and is dropped. Truncation can only shorten a key, never turn one
// key into another, so this is belt and braces - but it costs a line each.
func (p *parser) scan(b []byte, atStart, atEnd bool) {
	if len(b) == 0 {
		return
	}

	// bufio.Scanner is deliberately not used: a window of binary with no
	// newline in it is a single 128 KB "line", which is bufio.ErrTooLong and an
	// error path to get wrong. Splitting a slice already in memory has none.
	lines := bytes.Split(b, []byte{'\n'})
	if !atStart && len(lines) > 0 {
		lines = lines[1:]
	}
	if !atEnd && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		p.line(strings.TrimRight(string(line), "\r"))
	}
}

func (p *parser) line(raw string) {
	s := strings.TrimLeft(raw, " \t")
	if !strings.HasPrefix(s, ";") {
		return
	}
	s = strings.TrimSpace(s[1:])

	p.generator(s)

	// One rule, two branches, and the asymmetry is load-bearing.
	//
	// An `=` line is exactly one statement, and everything after the first `;`
	// in its value is a per-extruder list tail we do not want
	// (`filament_type = PETG;PETG;PLA`). Splitting it into further statements
	// would also mean a user who typed `temperature = 200` into their filament
	// notes could override the real temperature - Slic3r writes its config
	// alphabetically, so `filament_notes` is scanned first and first wins.
	//
	// A `:` line carries no user free text and really can hold two statements:
	// Bambu writes `model printing time: 42m 3s; total estimated time: 47m 18s`.
	if i := strings.IndexByte(s, '='); i >= 0 {
		p.put(s[:i], cut(s[i+1:], ';'))
		return
	}
	if strings.IndexByte(s, ':') < 0 {
		return
	}
	for _, seg := range strings.Split(s, ";") {
		if i := strings.IndexByte(seg, ':'); i >= 0 {
			p.put(seg[:i], seg[i+1:])
		}
	}
}

// put records a key we might look up later. Keys outside the candidate set are
// dropped, which is what keeps a 52 KB base64 thumbnail - whose lines end in
// `=` and so parse as enormous keys - from ever entering the map. First
// occurrence wins, and the head window is scanned first, so a header summary
// beats a footer repeat of the same key.
func (p *parser) put(key, value string) {
	k := strings.ToLower(strings.TrimSpace(key))
	if !candidates[k] {
		return
	}
	if _, ok := p.seen[k]; ok {
		return
	}
	v := strings.TrimSpace(value)
	if v == "" {
		return
	}
	p.seen[k] = v
}

// slicers maps the name a generator line carries to the name we display.
var slicers = map[string]string{
	"prusaslicer":      "PrusaSlicer",
	"superslicer":      "SuperSlicer",
	"orcaslicer":       "OrcaSlicer",
	"bambustudio":      "Bambu Studio",
	"cura_steamengine": "Cura",
	"cura":             "Cura",
}

// generator picks the slicer out of a comment payload.
//
// Two things here are load-bearing. The match is anchored at the start of the
// payload, so `; filament_notes = generated by PrusaSlicer 9.9` - free text a
// user can type - cannot name a slicer. And the first match in file order wins,
// because OrcaSlicer 1.5 writes a *fake* SuperSlicer generator line six lines
// below its real one, commented "hack-fix: write fake slicer info here so that
// preprocess_cancellation can process". Anything that checks for SuperSlicer
// first misreads every Orca file ever written.
func (p *parser) generator(s string) {
	if p.slicer != "" {
		return
	}
	rest, ok := cutPrefixFold(s, "generated by ")
	if !ok {
		if rest, ok = cutPrefixFold(s, "generated with "); !ok {
			return
		}
	}

	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return
	}
	name, version := fields[0], ""
	if len(fields) > 1 {
		version = fields[1]
	}
	// Bambu writes its version glued on with a hyphen -
	// `generated by BambuStudio-02.07.01.62` - where everyone else uses a
	// space. Only a hyphen followed by a digit splits, so OrcaSlicer's
	// `2.3.2-dev` version token stays whole.
	if i := strings.IndexByte(name, '-'); i >= 0 && i+1 < len(name) && isDigit(name[i+1]) {
		name, version = name[:i], name[i+1:]
	}

	display, known := slicers[strings.ToLower(name)]
	if !known {
		return
	}
	// "on" is the date that follows a version-less generator line, not a
	// version.
	if version == "on" {
		version = ""
	}
	p.slicer, p.version = display, version
}

func (p *parser) meta() Meta {
	m := Meta{
		Slicer:        p.slicer,
		SlicerVersion: p.version,
		InfillPattern: p.text("fill_pattern", "sparse_infill_pattern"),
		FilamentType:  p.text("filament_type"),
		PrinterModel:  p.text("printer_model", "target_machine.name"),

		LayerHeightMm: p.number("layer_height", "layer height"),
		InfillPercent: p.number("fill_density", "sparse_infill_density"),
		NozzleTempC: p.number("nozzle_temperature", "temperature",
			"nozzle_temperature_initial_layer", "first_layer_temperature"),

		WallLoops:    p.count("perimeters", "wall_loops"),
		TopLayers:    p.count("top_solid_layers", "top_shell_layers"),
		BottomLayers: p.count("bottom_solid_layers", "bottom_shell_layers"),

		// Only the space-separated statistics, never the underscored
		// `filament_cost` config key: that one is the price of a kilogram of
		// the spool (27.82 in the PrusaSlicer fixture), not what the print
		// costs (0.01).
		FilamentCost: p.number("total filament cost", "filament cost"),

		// Totals are summed across extruders where the settings above take the
		// first. A five-material print used all five spools, but the panel's
		// single temperature row can only be about one of them.
		FilamentGrams: p.sum("total filament used [g]", "total filament weight [g]", "filament used [g]"),
		FilamentMm:    p.sum("filament used [mm]", "total filament length [mm]"),

		PrintTimeSeconds: p.duration("estimated printing time (normal mode)",
			"total estimated time", "model printing time"),

		Supports: p.flag("support_material", "enable_support"),
	}

	m.BedTempC = p.bedTemp()

	// 0 means "no limit" in every Slic3r-lineage slicer, so reporting
	// "0 mm³/s" would say the opposite of what the file says.
	if v := p.number("filament_max_volumetric_speed", "max_volumetric_speed"); v != nil && *v != 0 {
		m.MaxVolumetricSpeed = v
	}

	// Cura's two unqualified keys are only consulted for a Cura file. `time`
	// especially is too generic to trust from anywhere else, and Cura is the
	// only slicer that writes filament length in metres rather than
	// millimetres.
	if m.Slicer == "Cura" {
		if m.PrintTimeSeconds == nil {
			m.PrintTimeSeconds = p.duration("time")
		}
		if m.FilamentMm == nil {
			if v := p.sum("filament used"); v != nil {
				mm := *v * 1000
				m.FilamentMm = &mm
			}
		}
	}
	return m
}

// bedTemp resolves the bed temperature across two different models of the
// world. Slic3r-lineage slicers have one bed_temperature. Orca and Bambu have
// one temperature per build plate and a curr_bed_type saying which plate is on
// the machine, so the plate has to be looked up before the number means
// anything. When the plate name is one we do not know - Bambu has renamed them
// between versions - the first-layer temperature is the honest fallback,
// because Orca writes it as a compatibility line on every file.
func (p *parser) bedTemp() *float64 {
	if v := p.number("bed_temperature"); v != nil {
		return v
	}
	if key, ok := plates[strings.ToLower(p.seen["curr_bed_type"])]; ok {
		if v := p.number(key); v != nil {
			return v
		}
	}
	return p.number("first_layer_bed_temperature")
}

var plates = map[string]string{
	"cool plate":         "cool_plate_temp",
	"pla plate":          "cool_plate_temp",
	"engineering plate":  "eng_plate_temp",
	"high temp plate":    "hot_plate_temp",
	"smooth pei plate":   "hot_plate_temp",
	"textured pei plate": "textured_plate_temp",
}

func (p *parser) text(keys ...string) string {
	for _, k := range keys {
		if v, ok := p.seen[k]; ok {
			return cut(v, ',')
		}
	}
	return ""
}

// number takes the first comma-separated element, which for a multi-material
// file is extruder one.
func (p *parser) number(keys ...string) *float64 {
	for _, k := range keys {
		v, ok := p.seen[k]
		if !ok {
			continue
		}
		if n, ok := leadingNumber(cut(v, ',')); ok {
			return &n
		}
	}
	return nil
}

// sum adds every comma-separated element, for the per-print totals where each
// element is one extruder's share of the same number.
func (p *parser) sum(keys ...string) *float64 {
	for _, k := range keys {
		v, ok := p.seen[k]
		if !ok {
			continue
		}
		total, any := 0.0, false
		for _, part := range strings.Split(v, ",") {
			if n, ok := leadingNumber(part); ok {
				total, any = total+n, true
			}
		}
		if any && isFinite(total) {
			return &total
		}
	}
	return nil
}

// count reads a whole number of things - walls, top layers. A fractional value
// is a misread rather than a rounding problem: nothing has 2.9 perimeters, so
// `2` would be a number we invented.
func (p *parser) count(keys ...string) *int {
	v := p.number(keys...)
	if v == nil || *v < 0 || *v > maxCount || math.Trunc(*v) != *v {
		return nil
	}
	n := int(*v)
	return &n
}

func (p *parser) flag(keys ...string) *bool {
	for _, k := range keys {
		v, ok := p.seen[k]
		if !ok {
			continue
		}
		switch strings.ToLower(v) {
		case "1", "true":
			t := true
			return &t
		case "0", "false":
			f := false
			return &f
		}
	}
	return nil
}

// duration reads either a bare count of seconds - Cura writes `;TIME:35` - or
// the `1d 2h 3m 4s` string everyone else writes.
func (p *parser) duration(keys ...string) *int {
	for _, k := range keys {
		v, ok := p.seen[k]
		if !ok {
			continue
		}
		if secs, ok := parseDuration(v); ok {
			return &secs
		}
	}
	return nil
}

func parseDuration(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}

	// A bare number is seconds. Checked first so "35" is not read as 35 of
	// nothing. This is the one place an exponent gets in, because ParseFloat
	// reads `1e300` where leadingNumber would stop at the `e`.
	if n, err := strconv.ParseFloat(s, 64); err == nil && isFinite(n) {
		if n < 0 || n > maxCount {
			return 0, false
		}
		return int(n), true
	}

	units := map[byte]int{'d': 86400, 'h': 3600, 'm': 60, 's': 1}
	total, any := 0, false
	for i := 0; i < len(s); {
		// Only whitespace separates terms - every slicer here writes
		// `1d 2h 3m 4s`. Skipping anything else would read `-1h` as an hour,
		// because the sign is simply not a digit.
		if s[i] == ' ' || s[i] == '\t' {
			i++
			continue
		}
		if !isDigit(s[i]) {
			return 0, false
		}
		j := i
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		n, err := strconv.Atoi(s[i:j])
		if err != nil || n > maxCount {
			return 0, false
		}
		if j >= len(s) {
			return 0, false
		}
		mult, ok := units[s[j]|0x20]
		if !ok {
			return 0, false
		}
		// Bounded above, so n*mult cannot itself overflow, and the running
		// total is checked before the next term is added. `4611686018427387904d`
		// would otherwise wrap to a negative print time.
		total, any = total+n*mult, true
		if total > maxCount {
			return 0, false
		}
		i = j + 1
	}
	return total, any
}

// leadingNumber reads the number a value starts with, so `15%`, `0.0083m` and
// `0.20` all parse with one function.
//
// It cannot return NaN or an infinity: it only ever hands ParseFloat a run of
// digits with at most one dot and a sign, and a run of digits too long to
// represent comes back as a range error rather than an infinity.
func leadingNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	i, dot := 0, false
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		i++
	}
	start := i
	for i < len(s) {
		if isDigit(s[i]) {
			i++
			continue
		}
		if s[i] == '.' && !dot {
			dot, i = true, i+1
			continue
		}
		break
	}
	if i == start {
		return 0, false
	}
	n, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// fields counts the settings, which is both what decides whether a file is
// worth storing at all and what the panel's footer reports.
func (m Meta) fields() int {
	set := []bool{
		m.LayerHeightMm != nil, m.InfillPercent != nil, m.InfillPattern != "",
		m.WallLoops != nil, m.TopLayers != nil, m.BottomLayers != nil,
		m.NozzleTempC != nil, m.BedTempC != nil, m.PrintTimeSeconds != nil,
		m.FilamentGrams != nil, m.FilamentMm != nil, m.FilamentType != "",
		m.FilamentCost != nil, m.MaxVolumetricSpeed != nil,
		m.PrinterModel != "", m.Supports != nil,
	}
	n := 0
	for _, ok := range set {
		if ok {
			n++
		}
	}
	return n
}

// candidates is every key any lookup above can ask for. put consults it, so
// nothing else is ever stored.
var candidates = func() map[string]bool {
	keys := []string{
		"layer_height", "layer height",
		"fill_density", "sparse_infill_density",
		"fill_pattern", "sparse_infill_pattern",
		"perimeters", "wall_loops",
		"top_solid_layers", "top_shell_layers",
		"bottom_solid_layers", "bottom_shell_layers",
		"nozzle_temperature", "temperature",
		"nozzle_temperature_initial_layer", "first_layer_temperature",
		"bed_temperature", "first_layer_bed_temperature", "curr_bed_type",
		"cool_plate_temp", "eng_plate_temp", "hot_plate_temp", "textured_plate_temp",
		"estimated printing time (normal mode)", "total estimated time",
		"model printing time", "time",
		"total filament used [g]", "total filament weight [g]", "filament used [g]",
		"filament used [mm]", "total filament length [mm]", "filament used",
		"filament_type", "total filament cost", "filament cost",
		"filament_max_volumetric_speed", "max_volumetric_speed",
		"printer_model", "target_machine.name",
		"support_material", "enable_support",
	}
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set
}()

func cut(s string, sep byte) string {
	if i := strings.IndexByte(s, sep); i >= 0 {
		return s[:i]
	}
	return s
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return s[len(prefix):], true
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// isFinite keeps NaN and infinities out of Meta. It guards the two places one
// can actually arise: strconv.ParseFloat accepts the literal word "NaN", which
// a duration value could be, and two individually-representable numbers can
// overflow to +Inf when summed across extruders. json.Marshal refuses to encode
// either, and since the caller marshals during upload, one weird G-code file
// would fail the whole upload rather than merely producing no settings.
func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
