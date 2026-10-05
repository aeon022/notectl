package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// isolate gives each test an empty HOME and pristine package state, so no
// real config or NOTECTL_* env var can leak in.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	mu.Lock()
	fileData, overrides, envEnabled = map[string]interface{}{}, map[string]interface{}{}, false
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		fileData, overrides, envEnabled = map[string]interface{}{}, map[string]interface{}{}, false
		mu.Unlock()
	})
	return home
}

func writeYAML(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "notectl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notectl.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPrecedenceOverrideEnvFileDefault(t *testing.T) {
	home := isolate(t)
	writeYAML(t, home, "joplin_api_url: http://file:1\n")
	Init()
	if got := JoplinAPIURL(); got != "http://file:1" {
		t.Fatalf("file value = %q", got)
	}
	t.Setenv("NOTECTL_JOPLIN_API_URL", "http://env:2")
	if got := JoplinAPIURL(); got != "http://env:2" {
		t.Errorf("env must beat file, got %q", got)
	}
	set("joplin_api_url", "http://override:3")
	if got := JoplinAPIURL(); got != "http://override:3" {
		t.Errorf("override must beat env, got %q", got)
	}
}

func TestEnvIgnoredUntilInit(t *testing.T) {
	isolate(t)
	t.Setenv("NOTECTL_DATA_DIR", "/should/not/leak")
	if Shared() {
		t.Error("env var leaked into config before Init() — tests would hit real data")
	}
	Init()
	if !Shared() {
		t.Error("env var must apply after Init()")
	}
}

func TestDefaults(t *testing.T) {
	home := isolate(t)
	if Source() != SourceObsidian {
		t.Errorf("default source = %q", Source())
	}
	if JoplinAPIURL() != "http://localhost:41184" {
		t.Errorf("default joplin url = %q", JoplinAPIURL())
	}
	if want := filepath.Join(home, "Documents", "Notes"); VaultPath() != want {
		t.Errorf("VaultPath = %q, want %q", VaultPath(), want)
	}
	if ExcludeFolders() != nil || MirrorEnabled() {
		t.Error("exclude/mirror must default off")
	}
}

func TestSourceRejectsUnknown(t *testing.T) {
	isolate(t)
	set("source", "evernote")
	if Source() != SourceObsidian {
		t.Errorf("unknown source must fall back to obsidian, got %q", Source())
	}
	set("source", "apple")
	if Source() != SourceApple {
		t.Errorf("Source = %q", Source())
	}
}

func TestSyncSources(t *testing.T) {
	isolate(t)
	cases := []struct {
		raw  string
		want []SourceType
	}{
		{"", []SourceType{SourceObsidian}},
		{"apple, joplin", []SourceType{SourceApple, SourceJoplin}},
		{"apple,apple,joplin", []SourceType{SourceApple, SourceJoplin}}, // de-duplicated
		{"bogus, apple", []SourceType{SourceApple}},                     // unknown dropped
		{"bogus", []SourceType{SourceObsidian}},                         // nothing valid → default
	}
	for _, c := range cases {
		set("sync_sources", c.raw)
		if got := SyncSources(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SyncSources(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestMirrorNeedsBothSides(t *testing.T) {
	isolate(t)
	for raw, want := range map[string]bool{
		"apple":                 false,
		"obsidian":              false,
		"apple,obsidian":        true,
		"apple,markdown":        true,
		"joplin,obsidian":       false,
		"joplin,apple,obsidian": true,
	} {
		set("sync_sources", raw)
		if got := MirrorSourcesConfigured(); got != want {
			t.Errorf("MirrorSourcesConfigured(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestBoolFromEnvAndYAML(t *testing.T) {
	home := isolate(t)
	writeYAML(t, home, "mirror_apple_obsidian: true\n")
	Init()
	if !MirrorEnabled() {
		t.Error("yaml bool true not read")
	}
	t.Setenv("NOTECTL_MIRROR_APPLE_OBSIDIAN", "1")
	if !MirrorEnabled() {
		t.Error(`env "1" must count as true`)
	}
	t.Setenv("NOTECTL_MIRROR_APPLE_OBSIDIAN", "no")
	if MirrorEnabled() {
		t.Error(`env "no" must be false`)
	}
}

func TestExcludeFoldersTrimsAndSkipsEmpty(t *testing.T) {
	isolate(t)
	set("exclude_folders", " Diary, ,Templates ,")
	if got, want := ExcludeFolders(), []string{"Diary", "Templates"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ExcludeFolders = %q, want %q", got, want)
	}
}

func TestDBPathPrecedence(t *testing.T) {
	home := isolate(t)
	if want := filepath.Join(home, ".local", "share", "notectl", "notes.db"); DBPath() != want {
		t.Errorf("default DBPath = %q, want %q", DBPath(), want)
	}
	set("db_path", "~/legacy/n.db")
	if want := filepath.Join(home, "legacy", "n.db"); DBPath() != want {
		t.Errorf("legacy db_path = %q, want %q", DBPath(), want)
	}
	custom := filepath.Join(home, "Dropbox", "notectl")
	set("data_dir", custom)
	if want := filepath.Join(custom, "notes.db"); DBPath() != want {
		t.Errorf("data_dir must beat db_path: %q, want %q", DBPath(), want)
	}
	if !Shared() {
		t.Error("data_dir set → Shared()")
	}
}

func TestSaveWritesFileAndFoldsOverrides(t *testing.T) {
	home := isolate(t)
	vault := filepath.Join(home, "Notes", "Vault")
	if err := Save(vault, SourceApple); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".config", "notectl", "notectl.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !contains(s, "vault_path: ~/Notes/Vault") || !contains(s, "source: apple") {
		t.Errorf("config file = %q (home must be contracted to ~)", s)
	}
	// overrides were folded into file data: a fresh Init() reads the same values
	mu.Lock()
	fileData, overrides = map[string]interface{}{}, map[string]interface{}{}
	mu.Unlock()
	Init()
	if Source() != SourceApple || VaultPath() != vault {
		t.Errorf("round trip: source=%q vault=%q", Source(), VaultPath())
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestVaultsFreeTierLimitAndUse(t *testing.T) {
	home := isolate(t)
	a, b := filepath.Join(home, "a"), filepath.Join(home, "b")
	if err := VaultAdd("work", a); err != nil {
		t.Fatalf("first vault must be free: %v", err)
	}
	if err := VaultAdd("work", a); err != nil {
		t.Errorf("re-adding the same name is not a second vault: %v", err)
	}
	if err := VaultAdd("home", b); err == nil {
		t.Fatal("second vault without a license must be refused")
	}
	set("license_status", "granted")
	set("license_benefit_id", bundleBenefitID)
	if err := VaultAdd("home", b); err != nil {
		t.Fatalf("licensed second vault: %v", err)
	}
	if got := Vaults()["home"]; got != "~/b" {
		t.Errorf("vault path stored as %q, want ~/b", got)
	}
	if err := VaultUse("home"); err != nil {
		t.Fatal(err)
	}
	if VaultPath() != b {
		t.Errorf("VaultUse: VaultPath = %q, want %q", VaultPath(), b)
	}
	if err := VaultUse("nope"); err == nil {
		t.Error("unknown vault must error")
	}
}

func TestIsProNeedsMatchingBenefit(t *testing.T) {
	isolate(t)
	if IsPro() {
		t.Error("no license must not be pro")
	}
	set("license_status", "granted")
	set("license_benefit_id", "some-other-product")
	if IsPro() {
		t.Error("key for a different product must not unlock notectl")
	}
	set("license_benefit_id", notectlBenefitID)
	if !IsPro() {
		t.Error("own benefit id must unlock")
	}
}

func TestExpandAndContractHome(t *testing.T) {
	home := isolate(t)
	if got := expandHome("~/x/y"); got != filepath.Join(home, "x", "y") {
		t.Errorf("expandHome = %q", got)
	}
	if expandHome("~other/x") != "~other/x" {
		t.Error("only ~/ is expanded")
	}
	if got := contractHome(filepath.Join(home, "x")); got != "~/x" {
		t.Errorf("contractHome = %q", got)
	}
	// a sibling dir that merely shares the home prefix must NOT be contracted
	sibling := home + "2/x"
	if got := contractHome(sibling); got != sibling {
		t.Errorf("contractHome(%q) = %q — prefix match without a path separator", sibling, got)
	}
}

func TestSetLicensePersists(t *testing.T) {
	home := isolate(t)
	if err := SetLicense("KEY", "granted", "b1"); err != nil {
		t.Fatal(err)
	}
	if LicenseKey() != "KEY" || LicenseStatus() != "granted" || LicenseBenefitID() != "b1" {
		t.Error("license fields not readable after SetLicense")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "notectl", "notectl.yaml")); err != nil {
		t.Errorf("license not written to disk: %v", err)
	}
}
