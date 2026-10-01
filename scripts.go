package main

import (
	"embed"
	"strings"
)

// Shell scripts live in scripts/ as ordinary files, so shellcheck and the
// tests can see them, and are built into the binary. Those that run on the
// host go through hostexec.go, the Amnezia probes run inside their containers.

//go:embed scripts
var scriptFS embed.FS

// script returns scripts/<name> with the shared libraries prepended.
func script(name string, libs ...string) string {
	var b strings.Builder
	for _, n := range append(libs, name) {
		data, err := scriptFS.ReadFile("scripts/" + n)
		if err != nil {
			panic(err) // embedded at build time, so a missing file is a bug
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

var (
	listScript      = script("list.sh")
	gitCheckScript  = script("git-check.sh", "lib-git.sh")
	appUpdateScript = script("app-update.sh", "lib-compose.sh")
	gitUpdateScript = script("git-update.sh", "lib-git.sh", "lib-compose.sh")
	wgScript        = script("wg.sh")
	openvpnScript   = script("openvpn.sh")
	telemtScript    = script("telemt.sh")

	// Long jobs of the Updates tab. The package scripts take the package
	// manager as the first argument.
	taskScripts = map[string]string{
		"check":   script("check.sh"),
		"install": script("pkg-install.sh"),
		"app":     appUpdateScript,
		"git":     gitUpdateScript,
	}
)
