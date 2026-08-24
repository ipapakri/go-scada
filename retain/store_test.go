package retain

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestStorePutGetAndRestart(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "retain.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Put("factory1.tank.level", []byte("first"))
	store.Put("factory1.tank.level", []byte("latest"))
	store.Put("factory1.tank.temperature", []byte("warm"))

	got, ok := store.Get("factory1.tank.level")
	if !ok || string(got) != "latest" {
		t.Fatalf("Get() = %q, %v", got, ok)
	}
	subjects := store.Subjects()
	sort.Strings(subjects)
	want := []string{"factory1.tank.level", "factory1.tank.temperature"}
	if !reflect.DeepEqual(subjects, want) {
		t.Fatalf("Subjects() = %v, want %v", subjects, want)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, ok = reopened.Get("factory1.tank.level")
	if !ok || string(got) != "latest" {
		t.Fatalf("reopened Get() = %q, %v", got, ok)
	}
}

func TestProtocolSubjects(t *testing.T) {
	t.Parallel()

	if GetSubject("factory1") != "retain.factory1.get" {
		t.Fatalf("GetSubject() = %q", GetSubject("factory1"))
	}
	if ListSubject("factory1") != "retain.factory1.list" {
		t.Fatalf("ListSubject() = %q", ListSubject("factory1"))
	}
}
