package dashboard

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func createTestDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE LiftingSession (
		id TEXT PRIMARY KEY,
		date TEXT NOT NULL,
		title TEXT NOT NULL,
		notes TEXT,
		source TEXT NOT NULL DEFAULT 'gravitus',
		createdAt TEXT NOT NULL,
		UNIQUE(date, source)
	)`)
	if err != nil {
		t.Fatalf("create LiftingSession table: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE Exercise (
		id TEXT PRIMARY KEY,
		sessionId TEXT NOT NULL,
		name TEXT NOT NULL,
		"order" INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		t.Fatalf("create Exercise table: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE ExerciseSet (
		id TEXT PRIMARY KEY,
		exerciseId TEXT NOT NULL,
		reps INTEGER NOT NULL DEFAULT 0,
		weightLbs REAL NOT NULL DEFAULT 0,
		"order" INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return path
}

func TestUpsertAndExists(t *testing.T) {
	path := createTestDB(t)
	date := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)

	sess := LiftingSession{
		Date:  date,
		Title: "Full Body + Core",
		Exercises: []ExerciseEntry{
			{Name: "Bench Press", Sets: []ExerciseSet{{Reps: 10, WeightLbs: 135}}},
		},
		Source: "gravitus",
	}

	if err := Upsert(path, sess); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	exists, err := ExistsOnDate(path, date, "gravitus")
	if err != nil {
		t.Fatalf("ExistsOnDate: %v", err)
	}
	if !exists {
		t.Error("ExistsOnDate = false after Upsert, want true")
	}

	// Upsert again — should update, not error
	sess.Title = "Updated"
	if err := Upsert(path, sess); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	// Different date → not found
	other := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	exists2, err := ExistsOnDate(path, other, "gravitus")
	if err != nil {
		t.Fatalf("ExistsOnDate (miss): %v", err)
	}
	if exists2 {
		t.Error("ExistsOnDate = true for absent date, want false")
	}
}

func TestTableExists(t *testing.T) {
	path := createTestDB(t)
	ok, err := TableExists(path)
	if err != nil {
		t.Fatalf("TableExists: %v", err)
	}
	if !ok {
		t.Error("TableExists = false after creating schema, want true")
	}
}

func TestNewCUID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		id := newCUID()
		if id == "" {
			t.Fatal("newCUID returned empty string")
		}
		if seen[id] {
			t.Fatalf("newCUID returned duplicate: %s", id)
		}
		seen[id] = true
	}
}
