package config

import (
	"reflect"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Addr:        ":8080",
		DataDir:     "./data",
		PublicURL:   "http://localhost:8080",
		AccessKey:   "local",
		SecretKey:   "localsecret",
		Region:      "us-east-1",
		Buckets:     []string{"laravel"},
		CORSOrigins: []string{"*"},
		LogFile:     "app.log",
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got  %+v\nwant %+v", c, want)
	}
}

func TestOverrides(t *testing.T) {
	c, err := FromEnv(env(map[string]string{
		"ADDR":                "127.0.0.1:9000",
		"DATA_DIR":            "/srv/data",
		"PUBLIC_URL":          "http://s3.test/",
		"AUTH":                "verify",
		"BUCKETS":             " app , media ,,",
		"AUTO_CREATE_BUCKETS": "true",
		"CORS_ORIGINS":        "http://app.test,http://admin.test",
		"LOG_FILE":            "off",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != "127.0.0.1:9000" || c.DataDir != "/srv/data" || c.PublicURL != "http://s3.test" ||
		!c.VerifyAuth || !c.AutoCreateBuckets || c.LogFile != "" {
		t.Fatalf("config = %+v", c)
	}
	if !reflect.DeepEqual(c.Buckets, []string{"app", "media"}) || len(c.CORSOrigins) != 2 {
		t.Fatalf("lists = %v %v", c.Buckets, c.CORSOrigins)
	}
}

func TestPublicURLFollowsPort(t *testing.T) {
	c, _ := FromEnv(env(map[string]string{"ADDR": ":9100"}))
	if c.PublicURL != "http://localhost:9100" {
		t.Fatal(c.PublicURL)
	}
}

func TestIgnoresLegacyStoragePath(t *testing.T) {
	// The old uploader stored plain files there; the store must not adopt it.
	c, _ := FromEnv(env(map[string]string{"STORAGE_PATH": "./old"}))
	if c.DataDir != "./data" {
		t.Fatal(c.DataDir)
	}
}

func TestInvalid(t *testing.T) {
	if _, err := FromEnv(env(map[string]string{"AUTH": "maybe"})); err == nil {
		t.Error("bad AUTH accepted")
	}
	if _, err := FromEnv(env(map[string]string{"AUTO_CREATE_BUCKETS": "sometimes"})); err == nil {
		t.Error("bad AUTO_CREATE_BUCKETS accepted")
	}
}
