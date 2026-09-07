package status

import (
	"os"
	"strings"
	"testing"
)

// 2.10.0 идёт после 2.9.0. Сравнение строк сказало бы обратное, и сказало бы
// это ровно один раз — в тот выпуск, когда номер перевалит за девять
// (amnezia-vpn-server-8bt5).
func TestIsNewerCountsNumbersNotLetters(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"2.9.0", "2.8.2", true},
		{"2.10.0", "2.9.0", true},
		{"2.9.0", "2.10.0", false},
		{"2.9.0", "2.9.0", false},
		{"2.8.2", "2.9.0", false},
		{"3.0.0", "2.99.99", true},
		{"v2.9.0", "2.8.2", true},
		// Версия, которую мы не смогли прочитать, — не версия, которую мы
		// предлагаем.
		{"", "2.9.0", false},
		{"latest", "2.9.0", false},
		{"2.9", "2.8.2", false},
		{"2.9.0", "", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.candidate, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, ожидалось %v", c.candidate, c.current, got, c.want)
		}
	}
}

// Контрольная сумма в описании выпуска предназначена агенту обновления.
// Человеку, пришедшему прочитать, что изменилось, она была бы шумом посреди
// текста.
func TestReleaseNotesLeaveOutTheChecksum(t *testing.T) {
	r := &Release{
		TagName: "v2.9.0",
		Body:    "- первое\n- второе\n\namnezia-sha256: 69c9ca13\n",
	}
	if got := r.Notes(); got != "- первое\n- второе" {
		t.Fatalf("Notes() = %q", got)
	}
	if got := r.Version(); got != "2.9.0" {
		t.Fatalf("Version() = %q", got)
	}
}

// Отсутствие файла — «неизвестно», а не сбой: так выглядит сервер, который
// ещё ни разу не спрашивал.
func TestReadersTreatAMissingFileAsNoOpinion(t *testing.T) {
	missing := t.TempDir() + "/nothing.json"
	if rel, err := ReadReleases(missing); err != nil || rel != nil {
		t.Fatalf("ReadReleases = %v, %v", rel, err)
	}
	if chk, err := ReadUpdateCheck(missing); err != nil || chk != nil {
		t.Fatalf("ReadUpdateCheck = %v, %v", chk, err)
	}
	if st, err := ReadUpdateState(missing); err != nil || st != nil {
		t.Fatalf("ReadUpdateState = %v, %v", st, err)
	}
	if d, err := ReadDeployment(missing); err != nil || d != nil {
		t.Fatalf("ReadDeployment = %v, %v", d, err)
	}
}

// Человек, отставший на три выпуска, должен прочитать все три: он решает,
// стоит ли обновляться, по тому, что изменится у него, а не по последней
// записи (amnezia-vpn-server-tjoq).
func TestNotesSinceGathersEveryReleaseInBetween(t *testing.T) {
	releases := []Release{
		{TagName: "v2.9.3", Body: "- третье\n\namnezia-sha256: c"},
		{TagName: "v2.9.2", Body: "- второе\n\namnezia-sha256: b"},
		{TagName: "v2.9.1", Body: "- первое\n\namnezia-sha256: a"},
		{TagName: "v2.9.0", Body: "- уже стоит"},
	}
	latest, notes := NotesSince(releases, "2.9.0")
	if latest != "2.9.3" {
		t.Fatalf("latest = %q, ожидалось 2.9.3", latest)
	}
	for _, want := range []string{"- первое", "- второе", "- третье"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("в описании нет %q:\n%s", want, notes)
		}
	}
	// Установленная версия и всё, что старше, не показывается.
	if strings.Contains(notes, "уже стоит") {
		t.Fatalf("показана запись установленной версии:\n%s", notes)
	}
	// Контрольные суммы предназначены агенту, не человеку.
	if strings.Contains(notes, "amnezia-sha256") {
		t.Fatalf("в описание попала контрольная сумма:\n%s", notes)
	}
}

// Свежая версия уже стоит — предлагать нечего.
func TestNotesSinceOffersNothingWhenUpToDate(t *testing.T) {
	releases := []Release{{TagName: "v2.9.3", Body: "x"}}
	if latest, notes := NotesSince(releases, "2.9.3"); latest != "" || notes != "" {
		t.Fatalf("NotesSince = %q, %q, ожидалось пусто", latest, notes)
	}
}

// Во время обновления панель уже новая, а файл ещё прежний: там лежит один
// объект, а не список.
func TestReadReleasesToleratesTheOlderShape(t *testing.T) {
	dir := t.TempDir()
	one := dir + "/one.json"
	if err := os.WriteFile(one, []byte(`{"tag_name":"v2.9.1","body":"- одно"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadReleases(one)
	if err != nil || len(got) != 1 || got[0].Version() != "2.9.1" {
		t.Fatalf("ReadReleases(один объект) = %v, %v", got, err)
	}

	many := dir + "/many.json"
	if err := os.WriteFile(many, []byte(`[{"tag_name":"v2.9.2"},{"tag_name":"v2.9.1"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = ReadReleases(many)
	if err != nil || len(got) != 2 {
		t.Fatalf("ReadReleases(список) = %v, %v", got, err)
	}
}
