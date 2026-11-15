package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	mg "go.mongodb.org/mongo-driver/v2/mongo"
)

// UpsertUser creates a MongoDB user in the authSource database with every role
// in roles granted on database, or updates the password and roles when the user
// already exists. An empty roles list defaults to readWrite on database.
func (s *Session) UpsertUser(ctx context.Context, username, password, database, authSource string, roles []string) error {
	if len(roles) == 0 {
		roles = []string{"readWrite"}
	}
	bsonRoles := make(bson.A, 0, len(roles))
	for _, role := range roles {
		bsonRoles = append(bsonRoles, bson.D{
			{Key: "role", Value: role},
			{Key: "db", Value: database},
		})
	}
	create := bson.D{
		{Key: "createUser", Value: username},
		{Key: "pwd", Value: password},
		{Key: "roles", Value: bsonRoles},
	}
	err := s.client.Database(authSource).RunCommand(ctx, create).Err()
	if err == nil {
		return nil
	}
	if !userAlreadyExists(err) {
		return err
	}
	update := bson.D{
		{Key: "updateUser", Value: username},
		{Key: "pwd", Value: password},
		{Key: "roles", Value: bsonRoles},
	}
	return s.client.Database(authSource).RunCommand(ctx, update).Err()
}

// userAlreadyExists reports the MongoDB error for createUser on an existing
// user (code 51003, "User ... already exists").
func userAlreadyExists(err error) bool {
	var ce mg.CommandError
	return errors.As(err, &ce) && ce.Code == 51003
}
