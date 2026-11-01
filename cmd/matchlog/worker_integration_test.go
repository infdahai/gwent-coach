package main

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"testing"

	"gwentcoachmvp/internal/training"
)

func TestSealedImportFeedsWorker(t *testing.T) {
	root := t.TempDir()
	inbox := filepath.Join(root, "inbox")
	store := filepath.Join(root, "store")
	empty := ""
	item := remoteItem{Name: "m1", Complete: true, Data: base64.StdEncoding.EncodeToString([]byte(valid)), Training: &empty}
	if err := importItems(inbox, []remoteItem{item}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	c := training.Config{Inbox: inbox, Store: store, RulesVersion: "native-v1", CatalogVersion: "14.10.0", PolicyVersion: "action-ranker-v1"}
	for _, want := range []string{"imported", "duplicate"} {
		reports, err := training.Run(c)
		if err != nil {
			t.Fatal(err)
		}
		if len(reports) != 1 || reports[0].Status != want {
			t.Fatalf("want %s: %+v", want, reports)
		}
	}
}
