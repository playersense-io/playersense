package main

import (
	"testing"

	"playersense.dev/backend/internal/testsupport"
)

func TestAppBuilds(t *testing.T) {
	settings := testsupport.Settings(t)
	app, err := build(settings)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := app.Build(); err != nil {
		t.Fatalf("the application does not assemble: %v", err)
	}
}

// A schedule that does not parse must stop the service at start-up rather than be
// substituted for.
//
// This is the one setting where a bad value is invisible at run time: the process starts,
// the health probe answers, the log says the scheduler is running, and the derivation
// simply happens at a different hour than anybody intended. Failing to build is the only
// moment the mistake is cheap.
func TestAnUnreadableScheduleStopsTheService(t *testing.T) {
	settings := testsupport.Settings(t)
	settings.WorkerDeriveAt = "half past two"

	if _, err := build(settings); err == nil {
		t.Fatal("a service with an unreadable schedule should not build")
	}
}

// An unknown ARCHIVE_STORE must stop the service too, for the same reason.
//
// The worker and archive-writer must agree about which bucket exists: §60 expires the
// objects archive-writer wrote and §61 rewrites them. A worker that fell back to a default
// store would sweep an empty bucket while the real one grew, and would report success doing
// it — the failure this whole file is about, arriving through a different setting.
func TestAnUnknownObjectStoreStopsTheService(t *testing.T) {
	settings := testsupport.Settings(t)
	settings.ArchiveStore = "s3ish"

	if _, err := build(settings); err == nil {
		t.Fatal("a service pointed at a store that does not exist should not build")
	}
}
