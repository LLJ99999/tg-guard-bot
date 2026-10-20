package admin

import "testing"

func TestValidate(t *testing.T) {
	ok, err := validate(map[string]any{"cas": true, "flood_count": float64(5), "verify_mode": "join", "night_start": "23:30"})
	if err != nil || ok["flood_count"] != 5 || ok["cas"] != true {
		t.Fatalf("got %v %v", ok, err)
	}
	for _, bad := range []map[string]any{
		{"id": float64(1)}, {"night_perms": "x"}, {"flood_count": float64(1.5)}, {"flood_count": float64(-1)},
		{"verify_mode": "x"}, {"cas": "yes"}, {"night_end": "25:00"}, {},
	} {
		if _, err := validate(bad); err == nil {
			t.Fatalf("应拒绝 %v", bad)
		}
	}
}
