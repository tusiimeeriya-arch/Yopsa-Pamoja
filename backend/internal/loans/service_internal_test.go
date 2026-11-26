package loans

import "testing"

type sqlStateError struct{ code string }

func (e sqlStateError) Error() string    { return "database refused the statement" }
func (e sqlStateError) SQLState() string { return e.code }

func TestIsUniqueViolation(t *testing.T) {
	if !isUniqueViolation(sqlStateError{code: "23505"}) {
		t.Fatal("23505 must be reported as a unique violation")
	}
	if isUniqueViolation(sqlStateError{code: "23514"}) {
		t.Fatal("23514 is a check violation, not a unique one")
	}
	if isUniqueViolation(nil) {
		t.Fatal("nil is not a unique violation")
	}
}
