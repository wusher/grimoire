//go:build !unix && !windows

package grimoire

func repositoryIdentity(string) string { return "" }
