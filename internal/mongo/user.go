package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	mg "go.mongodb.org/mongo-driver/v2/mongo"

	"mongo-tui/internal/engine"
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

// UserCard reads one user's definition from their home database: the databases
// they can authenticate in, their mechanisms, and their role grants.
func (s *Session) UserCard(ctx context.Context, database, username string) (engine.UserCard, error) {
	var out struct {
		Users []struct {
			User           string `bson:"user"`
			DB             string `bson:"db"`
			Authentication struct {
				Mechanisms []struct {
					Mechanism string `bson:"mechanism"`
				} `bson:"mechanisms"`
			} `bson:"authentication"`
			Roles bson.A `bson:"roles"`
		} `bson:"users"`
	}
	err := s.client.Database(database).
		RunCommand(ctx, d(e("usersInfo", d(e("user", username), e("db", database))))).
		Decode(&out)
	if err != nil {
		return engine.UserCard{}, err
	}

	card := engine.UserCard{Username: username, Database: database}
	for _, user := range out.Users {
		if user.User != username || user.DB != database {
			continue
		}
		card.Exists = true
		for _, m := range user.Authentication.Mechanisms {
			if m.Mechanism != "" {
				card.Mechanisms = append(card.Mechanisms, m.Mechanism)
			}
		}
		for _, role := range user.Roles {
			if ref, ok := roleRef(role); ok {
				card.Roles = append(card.Roles, ref)
			}
		}
		break
	}
	return card, nil
}

// UpdateUserRoles replaces a user's role grants in their home database without
// touching the password. An empty roles list revokes every role.
func (s *Session) UpdateUserRoles(ctx context.Context, database, username string, roles []engine.RoleRef) error {
	bsonRoles := make(bson.A, 0, len(roles))
	for _, role := range roles {
		bsonRoles = append(bsonRoles, d(e("role", role.Role), e("db", role.DB)))
	}
	return s.client.Database(database).
		RunCommand(ctx, d(e("updateUser", username), e("roles", bsonRoles))).
		Err()
}

// roleRef decodes one {role, db} entry from a usersInfo roles array.
func roleRef(value any) (engine.RoleRef, bool) {
	doc, ok := stageDocument(value)
	if !ok {
		return engine.RoleRef{}, false
	}
	var ref engine.RoleRef
	for _, item := range doc {
		switch item.Key {
		case "role":
			ref.Role = stringValue(item.Value)
		case "db":
			ref.DB = stringValue(item.Value)
		}
	}
	if ref.Role == "" {
		return engine.RoleRef{}, false
	}
	return ref, true
}
