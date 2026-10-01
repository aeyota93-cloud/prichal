package main

import (
	"strings"
	"testing"
	"time"
)

// Shaped like the real server output on 2026-09-30.
const sampleListing = `@@PM apt
@@INIT systemd
@@OS Ubuntu 24.04.3 LTS
@@KERNEL 6.8.0-142-generic
@@STAMP 1790755577
@@REBOOT
linux-image-6.8.0-143-generic
@@UPGRADABLE
Listing...
docker-ce/noble 5:29.8.1-1~ubuntu.24.04~noble amd64 [upgradable from: 5:29.4.3-1~ubuntu.24.04~noble]
libheif1/noble-updates,noble-security 1.17.6-1ubuntu4.9 amd64 [upgradable from: 1.17.6-1ubuntu4.8]
linux-image-generic/noble-updates,noble-security 6.8.0-143.143 amd64 [upgradable from: 6.8.0-142.142]
base-files/noble-updates 13ubuntu10.5 amd64 [upgradable from: 13ubuntu10.4]
@@SUMMARY
docker-ce	Docker: the open-source application container engine
libheif1	HEIF and AVIF file format decoder and encoder
base-files	Debian base system miscellaneous files
`

func TestParseListing(t *testing.T) {
	v := parseListing(sampleListing)
	if v.PM != "apt" || !v.Supported || v.Init != "systemd" || v.OS != "Ubuntu 24.04.3 LTS" {
		t.Fatalf("system: %+v", v)
	}
	if v.ListsAt != 1790755577 || !v.RebootRequired || len(v.RebootPkgs) != 1 {
		t.Fatalf("header: %+v", v)
	}
	want := map[string]string{"docker-ce": "docker", "libheif1": "security", "linux-image-generic": "kernel", "base-files": "other"}
	if len(v.Packages) != len(want) {
		t.Fatalf("got %d packages: %+v", len(v.Packages), v.Packages)
	}
	for _, p := range v.Packages {
		if want[p.Name] != p.Group {
			t.Errorf("%s: group %q, want %q", p.Name, p.Group, want[p.Name])
		}
	}
	d := v.Packages[1] // sorted: base-files, docker-ce, ...
	if d.Name != "docker-ce" || d.From != "5:29.4.3-1~ubuntu.24.04~noble" || d.To != "5:29.8.1-1~ubuntu.24.04~noble" || !strings.HasPrefix(d.Summary, "Docker") {
		t.Errorf("docker-ce parsed as %+v", d)
	}
	if !v.Packages[3].Security { // linux-image-generic comes from -security too
		t.Errorf("kernel security flag lost: %+v", v.Packages[3])
	}
}

const dnfListing = `@@PM dnf
@@INIT systemd
@@OS Rocky Linux 9.4 (Blue Onyx)
@@KERNEL 5.14.0-427.13.1.el9_4.x86_64
@@STAMP 1790700000
@@REBOOT
@@UPGRADABLE

openssl-libs.x86_64          1:3.0.7-28.el9_4          baseos
kernel-core.x86_64           5.14.0-427.16.1.el9_4     baseos
docker-ce.x86_64             3:27.1.1-1.el9            docker-ce-stable
Obsoleting Packages
grub2-tools.x86_64           1:2.06-80.el9             baseos
@@SECURITY
RLSA-2024:3061 Important/Sec. openssl-libs-1:3.0.7-28.el9_4.x86_64
@@INSTALLED
openssl-libs.x86_64	3.0.7-27.el9	A general purpose cryptography library with TLS implementation
kernel-core.x86_64	5.14.0-427.13.1.el9_4	The Linux kernel
docker-ce.x86_64	27.1.0-1.el9	The open-source application container engine
`

func TestParseDNF(t *testing.T) {
	v := parseListing(dnfListing)
	if v.PM != "dnf" || !v.Supported || !v.RebootRequired || len(v.Packages) != 3 {
		t.Fatalf("got %+v", v)
	}
	byName := map[string]PkgUpdate{}
	for _, p := range v.Packages {
		byName[p.Name] = p
	}
	if p := byName["openssl-libs.x86_64"]; !p.Security || p.Group != "security" || p.From != "3.0.7-27.el9" || p.To != "1:3.0.7-28.el9_4" {
		t.Errorf("openssl: %+v", p)
	}
	if byName["kernel-core.x86_64"].Group != "kernel" || byName["docker-ce.x86_64"].Group != "docker" {
		t.Errorf("groups: %+v", v.Packages)
	}
}

const apkListing = `@@PM apk
@@INIT other
@@OS Alpine Linux v3.20
@@KERNEL 6.6.31-0-virt
@@STAMP 1790700000
@@UPGRADABLE
Installed:                                Available:
busybox-1.36.1-r29                      < 1.36.1-r30
docker-26.1.3-r1                        < 26.1.5-r0
linux-virt-6.6.31-r0                    < 6.6.32-r0
`

func TestParseAPK(t *testing.T) {
	v := parseListing(apkListing)
	if v.PM != "apk" || !v.Supported || v.Init != "other" || len(v.Packages) != 3 {
		t.Fatalf("got %+v", v)
	}
	if p := v.Packages[0]; p.Name != "busybox" || p.From != "1.36.1-r29" || p.To != "1.36.1-r30" || p.Group != "other" {
		t.Errorf("busybox: %+v", p)
	}
	if v.Packages[1].Group != "docker" || v.Packages[2].Group != "kernel" {
		t.Errorf("groups: %+v", v.Packages)
	}
}

func TestUnsupportedPM(t *testing.T) {
	if v := parseListing("@@PM pacman\n@@INIT systemd\n"); v.Supported || v.PM != "pacman" {
		t.Errorf("pacman must be reported as unsupported: %+v", v)
	}
}

func TestKernelGroup(t *testing.T) {
	for name, want := range map[string]string{
		"linux-image-6.8.0-143-generic": "kernel", "linux-generic": "kernel",
		"linux-libc-dev": "other", "linux-firmware": "other",
	} {
		if got := pkgGroup(name, false); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
}

func TestPkgNameRejectsInjection(t *testing.T) {
	for _, bad := range []string{"-o", "foo;rm -rf /", "a b", "$(x)", ""} {
		if pkgName.MatchString(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
	for _, ok := range []string{"docker-ce", "libstdc++6", "containerd.io"} {
		if !pkgName.MatchString(ok) {
			t.Errorf("%q must be accepted", ok)
		}
	}
}

func TestDigestSlot(t *testing.T) {
	// Wednesday 2026-09-30 13:00 MSK -> Sunday 2026-09-27 12:00 MSK.
	got := lastDigestSlot(time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 9, 27, 12, 0, 0, 0, msk); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// Sunday 11:00 MSK is before the slot -> the previous Sunday.
	got = lastDigestSlot(time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 9, 27, 12, 0, 0, 0, msk); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseGitCheck(t *testing.T) {
	g, msg := parseGitCheck("BRANCH main\nCURRENT 83eebb0\nLATEST a1b2c3d\nBEHIND 2\nAHEAD 0\nDIRTY 1\nLOG Вторая правка\nLOG Первая правка\n")
	if msg != "" || g == nil || g.Behind != 2 || g.Current != "83eebb0" || g.Latest != "a1b2c3d" || !g.Dirty || g.Diverged || len(g.Commits) != 2 || g.Commits[0] != "Вторая правка" {
		t.Fatalf("got %+v %q", g, msg)
	}
	if g, msg := parseGitCheck("BRANCH main\nCURRENT a\nLATEST b\nBEHIND 1\nAHEAD 1\nDIRTY 0\n"); msg != "" || !g.Diverged {
		t.Fatalf("diverged not detected: %+v", g)
	}
	if g, msg := parseGitCheck("SKIP\n"); g != nil || msg != "" {
		t.Fatalf("SKIP: %+v %q", g, msg)
	}
	if g, msg := parseGitCheck("ERR нет сети\n"); g != nil || msg != "нет сети" {
		t.Fatalf("ERR: %+v %q", g, msg)
	}
}
