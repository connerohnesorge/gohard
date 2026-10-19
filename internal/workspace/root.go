// Package workspace finds the VHDL files of a project (#20): the project
// root, file discovery from the root or from command-line paths, symbolic
// link resolution and loop detection, duplicate removal and the file
// size limit. Later it also holds library mapping and snapshots (#34).
//
// Everything goes through env.FS, so discovery is a pure function of the
// file tree it sees: the result is sorted, independent of the order of
// the paths given, and of the job runner used to read the files.
package workspace

// ConfigName is the project configuration file (#28). The project root
// itself is found by config.FindRoot, the single root rule.
const ConfigName = "gohard.yaml"
