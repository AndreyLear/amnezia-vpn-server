package status

import "testing"

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
	if rel, err := ReadRelease(missing); err != nil || rel != nil {
		t.Fatalf("ReadRelease = %v, %v", rel, err)
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
