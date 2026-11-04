package model

import "regexp"

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// ValidID reports whether s may name a tenant or a watch. IDs appear in file
// paths, routes and database keys, so they are lower-case letters, digits and
// hyphens only, 1 to 40 characters, starting with a letter or digit. That also
// rules out path separators and "..".
func ValidID(s string) bool { return idPattern.MatchString(s) }
