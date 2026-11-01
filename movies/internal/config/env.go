package config

import "os"

// Getenv reads the environment variable KUMOBOX_<name>.
func Getenv(name string) string { return os.Getenv("KUMOBOX_" + name) }
