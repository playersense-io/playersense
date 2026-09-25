package signing

import (
	"testing"
	"time"
)

func TestASignatureVerifiesAndATamperedOneDoesNot(t *testing.T) {
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	old, _ := NewSecret()
	at := time.Now()
	body := []byte(`{"type":"report.delivered"}`)
	header := Sign("msg_1", at, body, secret, old)
	ts := "" + itoa(at.Unix())

	if err := Verify(secret, "msg_1", ts, header, body, at); err != nil {
		t.Fatalf("the current secret: %v", err)
	}
	if err := Verify(old, "msg_1", ts, header, body, at); err != nil {
		t.Fatalf("the previous secret during a roll: %v", err)
	}
	if err := Verify(secret, "msg_1", ts, header, []byte(`{"type":"x"}`), at); err != ErrNoSignature {
		t.Fatalf("a changed body verified: %v", err)
	}
	if err := Verify(secret, "msg_2", ts, header, body, at); err != ErrNoSignature {
		t.Fatalf("a changed id verified: %v", err)
	}
	if err := Verify(secret, "msg_1", ts, header, body, at.Add(10*time.Minute)); err != ErrStale {
		t.Fatalf("a replay ten minutes later verified: %v", err)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
