# 3d Library - Product Brief

## What it is

A single-user, self-hosted web app that turns uploaded 3D-printing files into a browsable, searchable, taggable library - plus collections and slicer integrations.

## Who it's for

Home 3D-printing hobbyists with a growing pile of downloaded models, running a homelab (Unraid, Synology, Proxmox).

## The core idea (important for the rewrite)

**The database is the source of truth; uploaded files are managed by the application.** A Model is a database record containing one or more files. Models and files are uploaded through the web interface; the server stores those files in its managed storage and records their locations and derived metadata in the database. Files are added, moved, or deleted only through the application so storage and database state stay consistent.

That's the whole architecture in one paragraph, and it's the thing to preserve.

## Domain model

| Entity | Notes |
|---|---|
| **Model** | The central library record. Name, description, print tips, source URL, category, thumbnail, arbitrary `custom_meta` JSON. Self-referencing `parent_id` gives **versions** (v1/v2/final under one entry). |
| **File** | Belongs to a Model. Type (stl/3mf/gcode/step/obj/image/document), size, extracted metadata JSON, thumbnail. |
| **Collection** ("project") | Many-to-many bag of Models for multi-part builds. |
| **Category** (one per model, colored) / **Tag** (many) / **Material** (5 seeded presets + custom) | |

## Feature areas

**1. Library management**
Web upload supports up to 20 files at 500 MB each. An upload can create a model or add files to an existing model. The library uses a model grid with generated thumbnails and supports bulk delete and tagging; all file operations go through the application.

**2. Metadata extraction (the crown jewel)**
G-code header parsing with multi-slicer regex fallbacks (PrusaSlicer, Bambu, Orca, Cura, Klipper): layer height, infill % and pattern, print time, filament length/weight/type/cost, nozzle + bed temps, wall loops, top/bottom layers, max volumetric speed, printer model, supports. Thumbnails pulled out of embedded base64 PNGs in G-code and from `Metadata/*.png` inside 3MF zips. Only the first 16 KB and last 128 KB of a G-code file are read, which is what keeps this fast on gigabyte files.

**3. In-browser 3D viewing**
STL and 3MF mesh preview; G-code rendered layer-by-layer with a scrub slider, live Z-height, auto-framed camera, filament color matching, and a build-volume grid inferred from the detected printer model.

**4. Organization & search**
Search, filter by category/tag, sort, paginate (24/page). Multi-select with Ctrl/Shift-click for bulk tag, bulk recategorize, bulk delete, bulk add-to-collection. Duplicate finder: groups files by size, then SHA-256 hashes the candidates.

**6. Settings**
Settings tabs: categories, tags, materials, printers, maintenance, system, about. System log viewer. GitHub release check for update notifications. Dark/light plus themes (Glass, Industrial) and accent colors.
