package catalog

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestShortsFeedSessionRoundTrip(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	now := time.Now()
	ids := []string{"v3", "v1", "v2"}
	if err := cat.SaveShortsFeed(ctx, "token-1", ids, now); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, lastAccess, err := cat.LoadShortsFeed(ctx, "token-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// 洗牌顺序必须原样保存，游标才能稳定续播
	if len(loaded) != 3 || loaded[0] != "v3" || loaded[1] != "v1" || loaded[2] != "v2" {
		t.Fatalf("loaded ids = %#v, want shuffled order preserved", loaded)
	}
	if lastAccess.UnixMilli() != now.UnixMilli() {
		t.Fatalf("last access = %v, want %v", lastAccess.UnixMilli(), now.UnixMilli())
	}

	missing, _, err := cat.LoadShortsFeed(ctx, "missing")
	if err != nil || missing != nil {
		t.Fatalf("missing token = (%#v, %v), want (nil, nil)", missing, err)
	}

	touched := now.Add(time.Hour)
	if err := cat.TouchShortsFeed(ctx, "token-1", touched); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if _, lastAccess, _ = cat.LoadShortsFeed(ctx, "token-1"); lastAccess.UnixMilli() != touched.UnixMilli() {
		t.Fatalf("touched access = %v, want %v", lastAccess.UnixMilli(), touched.UnixMilli())
	}

	// 同 token 覆盖写
	if err := cat.SaveShortsFeed(ctx, "token-1", []string{"v9"}, touched); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if loaded, _, _ = cat.LoadShortsFeed(ctx, "token-1"); len(loaded) != 1 || loaded[0] != "v9" {
		t.Fatalf("overwritten ids = %#v, want [v9]", loaded)
	}

	if err := cat.DeleteShortsFeed(ctx, "token-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if loaded, _, _ = cat.LoadShortsFeed(ctx, "token-1"); loaded != nil {
		t.Fatalf("deleted token still loads %#v", loaded)
	}

	if err := cat.SaveShortsFeed(ctx, "", []string{"v1"}, now); err == nil {
		t.Fatal("empty token should be rejected")
	}
}

func TestPruneShortsFeedsByAgeAndCap(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	base := time.Now()
	for i := 0; i < 5; i++ {
		token := "token-" + strconv.Itoa(i)
		if err := cat.SaveShortsFeed(ctx, token, []string{"v"}, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("save %s: %v", token, err)
		}
	}

	// 按最近访问时间清理：早于 base+2min 的 token-0 / token-1 被删除
	if err := cat.PruneShortsFeeds(ctx, base.Add(2*time.Minute), 10); err != nil {
		t.Fatalf("prune by age: %v", err)
	}
	for i, wantAlive := range []bool{false, false, true, true, true} {
		ids, _, err := cat.LoadShortsFeed(ctx, "token-"+strconv.Itoa(i))
		if err != nil {
			t.Fatalf("load token-%d: %v", i, err)
		}
		if alive := ids != nil; alive != wantAlive {
			t.Fatalf("token-%d alive = %v, want %v", i, alive, wantAlive)
		}
	}

	// 按数量收敛：只保留最近访问的 2 个（token-3 / token-4）
	if err := cat.PruneShortsFeeds(ctx, base, 2); err != nil {
		t.Fatalf("prune by cap: %v", err)
	}
	for i, wantAlive := range []bool{false, false, false, true, true} {
		ids, _, err := cat.LoadShortsFeed(ctx, "token-"+strconv.Itoa(i))
		if err != nil {
			t.Fatalf("load token-%d: %v", i, err)
		}
		if alive := ids != nil; alive != wantAlive {
			t.Fatalf("token-%d alive = %v, want %v", i, alive, wantAlive)
		}
	}
}
