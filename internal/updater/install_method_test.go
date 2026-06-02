package updater

import "testing"

func TestDetectInstallMethod(t *testing.T) {
	cases := []struct {
		exePath string
		want    InstallMethod
	}{
		{"/opt/homebrew/Cellar/contexo/1.3.0/bin/ctx", InstallHomebrew},
		{"/usr/local/Cellar/contexo/1.3.0/bin/ctx", InstallHomebrew},
		{"/home/linuxbrew/.linuxbrew/bin/ctx", InstallHomebrew},
		{`C:\Users\me\scoop\apps\ctx\current\ctx.exe`, InstallScoop},
		{"/home/me/.local/bin/ctx", InstallManaged},
		{`C:\Users\me\go\bin\ctx.exe`, InstallManaged},
	}
	for _, c := range cases {
		if got := detectInstallMethod(c.exePath); got != c.want {
			t.Errorf("detectInstallMethod(%q) = %v, want %v", c.exePath, got, c.want)
		}
	}
}
