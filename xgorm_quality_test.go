package xgorm

import (
	"strings"
	"testing"
)

func TestOpenNilConfig(t *testing.T) {
	if _, err := Open(nil); err == nil {
		t.Fatalf("expected error when config is nil")
	}
}

func TestPostgresIndexUniqueRequiresBTree(t *testing.T) {
	idx := NewPostgresIndex().
		Table("users").
		Fields("email").
		Method(PostgresIndexGin).
		Unique(true)

	_, err := idx.ToSQL(nil)
	if err == nil {
		t.Fatalf("expected error for UNIQUE + non-BTREE method")
	}
	if !strings.Contains(err.Error(), "UNIQUE index is only supported with BTREE") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPostgresCleanupGhostDropPolicy(t *testing.T) {
	mgr := NewPostgresIndexManager(nil, nil)
	pattern := mgr.getManagedRegex()

	managedNames := map[string]bool{
		"idx_users_email": true,
	}

	if mgr.shouldDropGhostIndex("custom_manual_index", managedNames, pattern) {
		t.Fatalf("manual index without managed prefix should never be dropped")
	}

	if mgr.shouldDropGhostIndex("idx_users_email", managedNames, pattern) {
		t.Fatalf("existing managed index in desired set should not be dropped")
	}

	if !mgr.shouldDropGhostIndex("idx_users_email_old", managedNames, pattern) {
		t.Fatalf("stale managed index should be dropped")
	}

	mgr.WhiteList("idx_users_email_old")
	if mgr.shouldDropGhostIndex("idx_users_email_old", managedNames, pattern) {
		t.Fatalf("whitelisted index should not be dropped")
	}
}

func TestMysqlManagedRegexEscapesPrefixes(t *testing.T) {
	mgr := NewMysqlIndexManager(nil, nil)
	mgr.managedPrefixes = nil
	mgr.Prefixes("idx", "uk+", "custom.*")

	re := mgr.getManagedRegex()

	if !re.MatchString("idx_users_email_0123456789abcdef") {
		t.Fatalf("expected idx prefix to match managed pattern")
	}
	if !re.MatchString("uk+_users_email_0123456789abcdef") {
		t.Fatalf("expected escaped uk+ prefix to match literally")
	}
	if !re.MatchString("custom.*_users_email_0123456789abcdef") {
		t.Fatalf("expected escaped custom.* prefix to match literally")
	}
	if re.MatchString("customZZ_users_email_0123456789abcdef") {
		t.Fatalf("regex meta chars in prefix should not act as wildcards")
	}
}
