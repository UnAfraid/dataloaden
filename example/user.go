//go:generate go run github.com/UnAfraid/dataloaden -name UserLoader -fileName generated_user_loader.go -keyType string -valueType *github.com/UnAfraid/dataloaden/example.User

package example

// User is some kind of database backed model
type User struct {
	ID   string
	Name string
}
