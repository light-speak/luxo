package queue

import (
	"strconv"
	"testing"
)

func TestNATSDeliveryCount(t *testing.T) {
	for _, test := range []struct {
		subject string
		want    uint64
	}{
		{"$JS.ACK.stream.consumer.1.2.3.123456789.0", 1},
		{"$JS.ACK.domain.account.stream.consumer.100.2.3.123456789.0.token", 100},
		{"$JS.ACK._.account.stream.consumer.42.2.3.123456789.0", 42},
		{"$JS.ACK._.account.stream.consumer.42.2.3.123456789.0.token.extra", 42},
		{"$JS.ACK.stream.consumer.18446744073709551615.2.3.123456789.0", ^uint64(0)},
	} {
		got, err := natsDeliveryCount(test.subject)
		if err != nil || got != test.want {
			t.Fatal(test.subject, got, err)
		}
	}
	for _, subject := range []string{
		"", "$JS.ACK.stream.consumer.1", "$XX.ACK.stream.consumer.1.2.3.123456789.0",
		"$JS.BAD.stream.consumer.1.2.3.123456789.0", "$JS.ACK.stream.consumer.1.2.3.123456789.0.extra",
		"$JS.ACK.stream.consumer.0.2.3.123456789.0", "$JS.ACK.stream.consumer..2.3.123456789.0",
		"$JS.ACK.stream.consumer.-1.2.3.123456789.0", "$JS.ACK.stream.consumer.+1.2.3.123456789.0",
		"$JS.ACK.stream.consumer.1a.2.3.123456789.0", "$JS.ACK.stream.consumer.18446744073709551616.2.3.123456789.0",
	} {
		if value, err := natsDeliveryCount(subject); err == nil || value != 0 {
			t.Fatal("invalid delivery accepted", subject, value, err)
		}
	}
}

func FuzzNATSDeliveryCount(f *testing.F) {
	for _, value := range []string{"1", "42", "18446744073709551615", "18446744073709551616", "-1", "", "1.2"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, text string) {
		want, err := strconv.ParseUint(text, 10, 64)
		valid := err == nil && want > 0
		for _, r := range text {
			valid = valid && r >= '0' && r <= '9'
		}
		for _, prefix := range []string{"$JS.ACK.stream.consumer.", "$JS.ACK._.account.stream.consumer."} {
			got, err := natsDeliveryCount(prefix + text + ".2.3.123456789.0")
			// Dots may change the ACK layout, so test malformed token shapes
			// separately rather than treating them as a single delivery field.
			for _, r := range text {
				if r == '.' {
					return
				}
			}
			if valid {
				if err != nil || got != want {
					t.Fatal(text, got, want, err)
				}
			} else if err == nil {
				t.Fatal("invalid count accepted", text, got)
			}
		}
	})
}
