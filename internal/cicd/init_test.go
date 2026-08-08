package cicd_test

import (
	"bytes"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

const (
	templateModule     = "github.com/robert-crandall/3d-library"
	templateName       = "3D Library"
	templateSlug       = "3d-library"
	sourceTemplateSlug = "go-home-" + "template"
)

func TestInitRenamesTrackedTree(t *testing.T) {
	t.Run("bare init infers module and name", func(t *testing.T) {
		repo := copyTrackedTree(t)
		module := differentModule(
			t,
			templateModule,
			"github.com/example/renamed-home",
			"code.example/acme/alternate-home",
		)
		slug := path.Base(module)
		runGit(t, repo, "remote", "add", "origin", "https://"+module+".git")

		adrBefore := readRepoFile(t, repo, "docs/tech-stack.md")
		scriptBefore := readRepoFile(t, repo, "scripts/init.sh")
		out, err := runInit(repo)
		if err != nil {
			t.Fatalf("make init failed: %v\n%s", err, out)
		}

		assertSuccessfulRename(
			t, repo, out, adrBefore, scriptBefore,
			module, slug, slug,
		)
	})

	t.Run("explicit module and name override defaults", func(t *testing.T) {
		repo := copyTrackedTree(t)
		module := differentModule(
			t,
			templateModule,
			"git.example/acme/widget",
			"github.com/example/other-widget",
		)
		name := differentValue(templateName, "Widget Home", "Other Widget")
		slug := path.Base(module)
		if templateName == templateSlug {
			name = slug
		}

		adrBefore := readRepoFile(t, repo, "docs/tech-stack.md")
		scriptBefore := readRepoFile(t, repo, "scripts/init.sh")
		out, err := runInit(
			repo,
			"MODULE="+module,
			"NAME="+name,
		)
		if err != nil {
			t.Fatalf("make init failed: %v\n%s", err, out)
		}

		assertSuccessfulRename(
			t, repo, out, adrBefore, scriptBefore,
			module, name, slug,
		)
	})
}

func TestInitRejectsAmbiguousNameAfterBareRename(t *testing.T) {
	repo := copyTrackedTree(t)
	firstModule := differentModule(
		t,
		templateModule,
		"github.com/example/first-home",
		"code.example/acme/other-first-home",
	)
	runGit(t, repo, "remote", "add", "origin", "https://"+firstModule+".git")
	if out, err := runInit(repo); err != nil {
		t.Fatalf("first bare init failed: %v\n%s", err, out)
	}

	secondModule := differentModule(
		t,
		firstModule,
		"github.com/example/second-home",
		"code.example/acme/other-second-home",
	)
	out, err := runInit(
		repo,
		"MODULE="+secondModule,
		"NAME=Distinct Home",
	)
	if err == nil {
		t.Fatalf("ambiguous second init succeeded\n%s", out)
	}
	if !strings.Contains(out, "current APP_NAME and APP_SLUG are both") {
		t.Errorf("ambiguous second init did not explain the collision:\n%s", out)
	}
	assertRepoFileContains(t, repo, "go.mod", "module "+firstModule)
}

func TestInitScanCatchesLeftoverNextToADR(t *testing.T) {
	repo := copyTrackedTree(t)
	module := differentModule(
		t,
		templateModule,
		"github.com/example/scan-target",
		"code.example/acme/other-scan-target",
	)
	name := differentValue(templateName, "Scan Target", "Other Scan Target")
	if templateName == templateSlug {
		name = path.Base(module)
	}

	const neighbor = "docs/tech-stack-neighbor.md"
	if err := os.WriteFile(filepath.Join(repo, neighbor), []byte("neutral before rename\n"), 0o644); err != nil {
		t.Fatalf("write neighboring file: %v", err)
	}
	runGit(t, repo, "add", neighbor)

	scriptPath := filepath.Join(repo, "scripts", "init.sh")
	script := readRepoFile(t, repo, "scripts/init.sh")
	const scanStart = "if [ ${#stale[@]} -gt 0 ]; then\n"
	if bytes.Count(script, []byte(scanStart)) != 1 {
		t.Fatalf("scan insertion point occurs %d times, want 1", bytes.Count(script, []byte(scanStart)))
	}
	injection := "printf '%s\\n' \"$old_module\" > docs/tech-stack-neighbor.md\n\n" + scanStart
	script = bytes.Replace(script, []byte(scanStart), []byte(injection), 1)
	if err := os.WriteFile(scriptPath, script, 0o755); err != nil {
		t.Fatalf("instrument init script: %v", err)
	}

	out, err := runInit(
		repo,
		"MODULE="+module,
		"NAME="+name,
	)
	if err == nil {
		t.Fatalf("make init succeeded with a planted leftover\n%s", out)
	}
	t.Logf("negative control failed as required:\n%s", out)
	if !strings.Contains(out, "rename incomplete - the old identity survives in:") {
		t.Errorf("failure did not identify an incomplete rename:\n%s", out)
	}
	if !strings.Contains(out, neighbor) {
		t.Errorf("failure did not name neighboring leftover %q:\n%s", neighbor, out)
	}
	if strings.Contains(out, "docs/tech-stack.md") {
		t.Errorf("failure reported the intentionally excluded ADR:\n%s", out)
	}
	if strings.Contains(out, "leftover scan: clean") {
		t.Errorf("failed scan also reported clean:\n%s", out)
	}
}

func assertSuccessfulRename(
	t *testing.T,
	repo string,
	out string,
	adrBefore []byte,
	scriptBefore []byte,
	module string,
	name string,
	slug string,
) {
	t.Helper()

	combined := module + "|" + name + "|" + slug
	unchecked := 0
	for _, old := range []string{templateModule, templateName, templateSlug} {
		if strings.Contains(combined, old) {
			unchecked++
		}
	}
	if unchecked == 0 {
		if !strings.Contains(out, "==> leftover scan: clean") {
			t.Errorf("make init did not report a clean leftover scan:\n%s", out)
		}
	} else if unchecked == 3 {
		if !strings.Contains(out, "==> leftover scan: skipped") {
			t.Errorf("make init did not report its skipped leftover scan:\n%s", out)
		}
	} else if !strings.Contains(out, "==> leftover scan:") || !strings.Contains(out, "unchecked") {
		t.Errorf("make init did not report its partial leftover scan:\n%s", out)
	}
	if got := readRepoFile(t, repo, "docs/tech-stack.md"); !bytes.Equal(got, adrBefore) {
		t.Error("docs/tech-stack.md changed during rename")
	}
	if !bytes.Contains(adrBefore, []byte(sourceTemplateSlug)) {
		t.Fatal("ADR exclusion is vacuous: the source ADR does not contain the template slug")
	}
	if got := readRepoFile(t, repo, "scripts/init.sh"); !bytes.Equal(got, scriptBefore) {
		t.Error("scripts/init.sh rewrote itself")
	}

	assertNoOldIdentity(t, repo, combined)
	assertRepoFileContains(t, repo, "go.mod", "module "+module)
	assertRepoFileContains(t, repo, "Makefile", "APP_MODULE ?= "+module)
	assertRepoFileContains(t, repo, "Makefile", "APP_NAME   ?= "+name)
	assertRepoFileContains(t, repo, "Makefile", "APP_SLUG   ?= "+slug)
	assertRepoFileContains(t, repo, "cmd/mcp/main.go", `"`+module+`/internal/app"`)
	assertRepoFileContains(t, repo, "web/package.json", `"name": "`+slug+`"`)
	assertRepoFileContains(t, repo, "web/static/manifest.webmanifest", `"name": "`+name+`"`)
	assertRepoFileContains(t, repo, "web/static/manifest.webmanifest", `"short_name": "`+name+`"`)
	assertRepoFileContains(t, repo, "docs/openapi.json", `"title": "`+name+`"`)
	assertRepoFileContains(t, repo, "README.md", `import appmigrations "`+module+`/migrations"`)
	assertRepoFileContains(t, repo, "scripts/docker-smoke.sh", `IMAGE="${IMAGE:-`+slug+`:smoke}"`)
	assertRepoFileContains(t, repo, ".github/workflows/ci.yml", "POSTGRES_DB: "+slug+"_test")
}

func assertNoOldIdentity(t *testing.T, repo string, newIdentity string) {
	t.Helper()

	for _, file := range trackedFiles(t, repo) {
		if file == "docs/tech-stack.md" {
			continue
		}
		for _, old := range []string{templateModule, templateName, templateSlug} {
			if strings.Contains(newIdentity, old) {
				continue
			}
			if strings.Contains(file, old) {
				t.Errorf("tracked path %q still contains %q", file, old)
			}
		}
		raw := readRepoFile(t, repo, file)
		for _, old := range []string{templateModule, templateName, templateSlug} {
			if strings.Contains(newIdentity, old) {
				continue
			}
			if bytes.Contains(raw, []byte(old)) {
				t.Errorf("%s still contains %q", file, old)
			}
		}
	}
}

func differentModule(t *testing.T, current string, candidates ...string) string {
	t.Helper()

	for _, candidate := range candidates {
		if candidate != current && !strings.Contains(candidate, current) {
			return candidate
		}
	}
	t.Fatalf("no candidate module differs cleanly from %q", current)
	return ""
}

func differentValue(current string, candidates ...string) string {
	for _, candidate := range candidates {
		if candidate != current {
			return candidate
		}
	}
	return current + " Other"
}

func assertRepoFileContains(t *testing.T, repo string, file string, want string) {
	t.Helper()

	if got := readRepoFile(t, repo, file); !bytes.Contains(got, []byte(want)) {
		t.Errorf("%s does not contain %q", file, want)
	}
}

func copyTrackedTree(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	repo := t.TempDir()
	for _, file := range trackedFiles(t, root) {
		src := filepath.Join(root, filepath.FromSlash(file))
		dst := filepath.Join(repo, filepath.FromSlash(file))
		info, err := os.Lstat(src)
		if err != nil {
			t.Fatalf("stat %s: %v", file, err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("create parent for %s: %v", file, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(src)
			if err != nil {
				t.Fatalf("read symlink %s: %v", file, err)
			}
			if err := os.Symlink(target, dst); err != nil {
				t.Fatalf("copy symlink %s: %v", file, err)
			}
			continue
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if err := os.WriteFile(dst, raw, info.Mode().Perm()); err != nil {
			t.Fatalf("copy %s: %v", file, err)
		}
	}

	runGit(t, repo, "init", "-q")
	runGit(t, repo, "add", "--all")
	return repo
}

func trackedFiles(t *testing.T, repo string) []string {
	t.Helper()

	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list tracked files in %s: %v", repo, err)
	}

	var files []string
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) > 0 {
			files = append(files, string(raw))
		}
	}
	return files
}

func readRepoFile(t *testing.T, repo string, file string) []byte {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file)))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	return raw
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func runInit(repo string, args ...string) (string, error) {
	cmd := exec.Command("make", append([]string{"init"}, args...)...)
	cmd.Dir = repo
	cmd.Env = initTestEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func initTestEnv() []string {
	drop := map[string]bool{
		"APP_MODULE": true,
		"APP_NAME":   true,
		"APP_SLUG":   true,
		"MAKEFLAGS":  true,
		"MFLAGS":     true,
		"MODULE":     true,
		"NAME":       true,
	}
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !drop[key] {
			env = append(env, entry)
		}
	}
	return append(env, "LC_ALL=C")
}
