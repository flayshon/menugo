package migrate

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoad(t *testing.T) {
	fsys := fstest.MapFS{
		"000002_second.sql": {Data: []byte("CREATE TABLE b (id INT);")},
		"000010_tenth.sql":  {Data: []byte("CREATE TABLE c (id INT);")},
		"000001_first.sql":  {Data: []byte("CREATE TABLE a (id INT);")},
		"embed.go":          {Data: []byte("package migrations")},
	}

	migrations, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, m := range migrations {
		got = append(got, m.Name)
	}
	if want := "first,second,tenth"; strings.Join(got, ",") != want {
		t.Errorf("order = %v; want %s", got, want)
	}
	if migrations[2].Version != 10 {
		t.Errorf("version = %d; want 10", migrations[2].Version)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"bad name": {
			"create_users.sql": {Data: []byte("SELECT 1;")},
		},
		"uppercase name": {
			"000001_Create.sql": {Data: []byte("SELECT 1;")},
		},
		"zero version": {
			"000000_zero.sql": {Data: []byte("SELECT 1;")},
		},
		"duplicate version": {
			"000001_a.sql": {Data: []byte("SELECT 1;")},
			"01_b.sql":     {Data: []byte("SELECT 1;")},
		},
		"empty file": {
			"000001_empty.sql": {Data: []byte("  \n")},
		},
	}

	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(fsys); err == nil {
				t.Error("expected an error")
			}
		})
	}
}
