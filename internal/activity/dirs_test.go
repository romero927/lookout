package activity

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCommandDirs(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := []struct {
		cmd  string
		want []string
	}{
		{"cd /mnt/repo && git status", []string{"/mnt/repo"}},
		{"ls; cd sub/dir; make", []string{"/work/sub/dir"}},
		{`cd "/a b/c" && git -C ../other log`, []string{"/a b/c", "/other"}},
		{"cd ~/proj", []string{filepath.Join(home, "proj")}},
		{"(cd /x/y && ls)", []string{"/x/y"}},
		{"cd $HOME; cd -; echo abcd /z", nil},
		{"grep -rn cd .", nil},
	}
	for _, c := range cases {
		if got := commandDirs(c.cmd, "/work"); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestSessionDirs(t *testing.T) {
	s := &session{}
	for _, d := range []string{"/a", "/b", "/a"} {
		s.addDir(d)
	}
	if !reflect.DeepEqual(s.dirs, []string{"/b", "/a"}) {
		t.Errorf("dirs = %v", s.dirs)
	}
}
