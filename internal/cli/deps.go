package cli

import "io"

type Dependencies struct {
	ConfigPath  string
	ProfilesDir string
	AuthPath    string
	Stdout      io.Writer
	Stderr      io.Writer
}
