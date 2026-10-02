package authn

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("s3cret-p@ss")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "s3cret-p@ss") {
		t.Error("верный пароль не прошёл")
	}
	if CheckPassword(h, "wrong") {
		t.Error("неверный пароль прошёл")
	}
}

func TestToken(t *testing.T) {
	t1, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	t2, _ := NewToken()
	if t1 == t2 {
		t.Error("токены совпали")
	}
	if TokenHash(t1) == TokenHash(t2) {
		t.Error("хэши совпали")
	}
	if len(t1) < 40 {
		t.Errorf("токен подозрительно короткий: %d символов", len(t1))
	}
}
