package pressure

import "testing"

func TestParseResource(t *testing.T) {
	data := []byte(
		"some avg10=12.50 avg60=8.25 avg300=2.00 total=123456\n" +
			"full avg10=1.50 avg60=1.00 avg300=0.50 total=23456\n",
	)

	resource, err := parseResource(data)
	if err != nil {
		t.Fatalf("parse PSI: %v", err)
	}

	if resource.Some.Avg10 != 12.50 {
		t.Fatalf(
			"expected some avg10 12.50, got %.2f",
			resource.Some.Avg10,
		)
	}

	if resource.Full.Avg10 != 1.50 {
		t.Fatalf(
			"expected full avg10 1.50, got %.2f",
			resource.Full.Avg10,
		)
	}

	if resource.Some.Total != 123456 {
		t.Fatalf(
			"expected total 123456, got %d",
			resource.Some.Total,
		)
	}
}
