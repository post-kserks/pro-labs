package auth

import "strings"

// credential is a demo account. Passwords are stored in plain text on purpose:
// this is a university demo, not a production system.
type credential struct {
	Password string
	User     User
}

// demoUsers are the fixed accounts described in the MedVault spec.
var demoUsers = map[string]credential{
	"doctor@clinic.ru": {
		Password: "demo123",
		User:     User{Email: "doctor@clinic.ru", Name: "Иван Петров", Role: "doctor", DoctorID: 1},
	},
	"admin@clinic.ru": {
		Password: "demo123",
		User:     User{Email: "admin@clinic.ru", Name: "Администратор", Role: "admin"},
	},
	"receptionist@clinic.ru": {
		Password: "demo123",
		User:     User{Email: "receptionist@clinic.ru", Name: "Регистратор", Role: "receptionist"},
	},
}

// Authenticate checks credentials and returns the matching user.
func Authenticate(email, password string) (User, bool) {
	cred, ok := demoUsers[strings.ToLower(strings.TrimSpace(email))]
	if !ok || cred.Password != password {
		return User{}, false
	}
	return cred.User, true
}
