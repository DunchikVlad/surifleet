// log_rotation_test.go — маппинги входов API для log_rotation и packages
// (чанк 111): чистые функции protoLogRotationAction / protoPackageAction.
package httpapi

import "testing"

func TestProtoLogRotationAction(t *testing.T) {
	report, ok := protoLogRotationAction("report")
	if !ok || !report {
		t.Fatalf("report: got %v %v", report, ok)
	}
	rotate, ok := protoLogRotationAction("rotate")
	if !ok || rotate {
		t.Fatalf("rotate: got %v %v", rotate, ok)
	}
	for _, bad := range []string{"", "rot", "ROTATE", "delete"} {
		if _, ok := protoLogRotationAction(bad); ok {
			t.Fatalf("%q: мусор не должен проходить", bad)
		}
	}
}

func TestProtoPackageAction(t *testing.T) {
	cases := map[string]bool{
		"check":   true,
		"install": true,
		"remove":  true,
		"update":  true,
	}
	for in, wantOK := range cases {
		_, ok := protoPackageAction(in)
		if ok != wantOK {
			t.Fatalf("%q: ok=%v", in, ok)
		}
	}
	for _, bad := range []string{"", "upgrade", "purge", "CHECK"} {
		if _, ok := protoPackageAction(bad); ok {
			t.Fatalf("%q: мусор не должен проходить", bad)
		}
	}
}
