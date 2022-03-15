//go:generate go run github.com/UnAfraid/dataloaden/v2 -name UserLoader -fileName generated_user_loader.go -keyType string -valueType *github.com/UnAfraid/dataloaden/v2/example.User

package example

// User is some kind of database backed model
type User struct {
	ID   string
	Name string
}
