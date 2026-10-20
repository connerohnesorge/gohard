# testdata/libsguide: the docs/libraries.md examples as projects

Each folder is one worked example of `docs/libraries.md` (#1): a
`gohard.yaml` and the VHDL the guide shows. `TestLibrariesGuide` in
`internal/cli` runs `gohard check` in every folder and fails on any
diagnostic, so a `use` that does not resolve breaks CI. It also checks
that the guide shows exactly these VHDL files and maps the same
libraries.

The vendor examples map the CI-only stubs in `testdata/stubs/`; OSVVM,
UVVM and VUnit map the pinned submodules in `testdata/libs/`. In your
own project, map your vendor install's folders as the guide shows.
