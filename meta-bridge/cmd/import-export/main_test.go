package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanThreadName_RemovesNumericSuffix(t *testing.T) {
	if got := cleanThreadName("Chat Name_123"); got != "Chat Name" {
		t.Fatalf("expected %q, got %q", "Chat Name", got)
	}
	if got := cleanThreadName("NoSuffix"); got != "NoSuffix" {
		t.Fatalf("expected %q, got %q", "NoSuffix", got)
	}
}

func TestThreadIDFromConversationPath(t *testing.T) {
	id, ok := threadIDFromConversationPath("messages/inbox/someone_1234567890")
	if !ok {
		t.Fatalf("expected ok")
	}
	if id != 1234567890 {
		t.Fatalf("expected %d, got %d", 1234567890, id)
	}
}

func TestIsFacebookExportZip_DetectsMessageFiles(t *testing.T) {
	dir := t.TempDir()

	fbZipPath := filepath.Join(dir, "fb.zip")
	{
		f, err := os.Create(fbZipPath)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		zw := zip.NewWriter(f)
		w, err := zw.Create("your_facebook_activity/messages/inbox/test_123/message_1.json")
		if err != nil {
			t.Fatalf("create entry: %v", err)
		}
		_, _ = w.Write([]byte(`{}`))
		_ = zw.Close()
		_ = f.Close()
	}

	if !isFacebookExportZip(fbZipPath) {
		t.Fatalf("expected Facebook export ZIP to be detected")
	}

	msgZipPath := filepath.Join(dir, "msg.zip")
	{
		f, err := os.Create(msgZipPath)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		zw := zip.NewWriter(f)
		w, err := zw.Create("conversation.json")
		if err != nil {
			t.Fatalf("create entry: %v", err)
		}
		_, _ = w.Write([]byte(`{"threadName":"x","participants":[],"messages":[]}`))
		_ = zw.Close()
		_ = f.Close()
	}

	if isFacebookExportZip(msgZipPath) {
		t.Fatalf("expected Messenger app export ZIP to not be detected as Facebook export")
	}
}
