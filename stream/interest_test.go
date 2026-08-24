package stream

import "testing"

func TestLastToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		subject string
		want    string
	}{
		{subject: "factory1.plant.tank.level.alert", want: "alert"},
		{subject: "factory1.plant.tank.level.alert_config", want: "alert_config"},
		{subject: "completed", want: "completed"},
		{subject: "  orders.eu.greece.retail.completed ", want: "completed"},
		{subject: "", want: ""},
	}
	for _, test := range tests {
		if got := LastToken(test.subject); got != test.want {
			t.Errorf("LastToken(%q) = %q, want %q", test.subject, got, test.want)
		}
	}
}

func TestDecodeSubjectCreatedFillsSuffix(t *testing.T) {
	t.Parallel()

	created, err := DecodeSubjectCreated(
		`{"subject":"factory1.plant.tank.level.alert_config"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.Subject != "factory1.plant.tank.level.alert_config" {
		t.Errorf("Subject = %q", created.Subject)
	}
	if created.Suffix != "alert_config" {
		t.Errorf("Suffix = %q", created.Suffix)
	}
}

func TestWorkerIndexIsStable(t *testing.T) {
	t.Parallel()

	first := workerIndex("factory1.plant.tank.level", DefaultInterestWorkers)
	second := workerIndex("factory1.plant.tank.level", DefaultInterestWorkers)
	if first != second {
		t.Fatalf("workerIndex was not stable: %d vs %d", first, second)
	}
	if first < 0 || first >= DefaultInterestWorkers {
		t.Fatalf("workerIndex out of range: %d", first)
	}
	other := workerIndex("factory2.plant.tank.level", DefaultInterestWorkers)
	if other < 0 || other >= DefaultInterestWorkers {
		t.Fatalf("workerIndex out of range: %d", other)
	}
}
