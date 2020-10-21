// +build integration

package test

import (
	"fmt"
	"testing"

	uuid "github.com/kthomas/go.uuid"
	provide "github.com/provideservices/provide-go/api/ident"
)

func TestCreateUser(t *testing.T) {
	tt := []struct {
		firstName string
		lastName  string
		email     string
		password  string
	}{
		{"first", "last", "first.last@email.com", "secrit_password"},
		{"joey", "joe joe", "j.j.j@email.com", "joeyjoejoe"},
	}

	for _, tc := range tt {
		user, err := userFactory(tc.firstName, tc.lastName, tc.email, tc.password)
		if err != nil {
			t.Errorf("user creation failed. Error: %s", err.Error())
			return
		}

		if user == nil {
			t.Errorf("no user returned")
			return
		}
	}
}

func TestAuthenticateUser(t *testing.T) {
	tt := []struct {
		firstName string
		lastName  string
		email     string
		password  string
	}{
		{"first", "last", "first.last.auth@email.com", "secrit_password"},
		{"joey", "joe joe", "j.j.j.auth@email.com", "joeyjoejoe"},
	}

	for _, tc := range tt {
		_, err := userFactory(tc.firstName, tc.lastName, tc.email, tc.password)
		if err != nil {
			t.Errorf("user creation failed. Error: %s", err.Error())
			return
		}

		auth, err := provide.Authenticate(tc.email, tc.password)
		if err != nil {
			t.Errorf("user authentication failed. Error: %s", err.Error())
		}

		if auth == nil {
			t.Errorf("user not authenticated")
		}
	}
}

func TestUserDetails(t *testing.T) {

	testId, err := uuid.NewV4()
	if err != nil {
		t.Logf("error creating new UUID")
	}

	tt := []struct {
		firstName string
		lastName  string
		email     string
		password  string
	}{
		{"first", "last", "first.last." + testId.String() + "@email.com", "secrit_password"},
		{"joey", "joe joe", "j.j" + testId.String() + "@email.com", "joeyjoejoe"},
		{"joey2", "joe joe2", "j.j2" + testId.String() + "@email.com", "joeyjoejoe2"},
	}

	for _, tc := range tt {
		// create the user
		user, err := userFactory(tc.firstName, tc.lastName, tc.email, tc.password)
		if err != nil {
			t.Errorf("user creation failed. Error: %s", err.Error())
			return
		}

		// get the auth token
		auth, err := provide.Authenticate(tc.email, tc.password)
		if err != nil {
			t.Errorf("user authentication failed for user %s. error: %s", tc.email, err.Error())
			return
		}

		// get the user details
		deets, err := provide.GetUserDetails(*auth.Token.Token, user.ID.String(), map[string]interface{}{})
		if err != nil {
			t.Errorf("error getting details for user id %s. Error: %s", user.ID, err.Error())
			return
		}

		// fail if they're not the same!
		if deets.Email != user.Email {
			t.Errorf("user email not returned correctly. expected %s, got %s", user.Email, deets.Email)
			return
		}

		if deets.FirstName != user.FirstName {
			t.Errorf("user first name not returned correctly. expected %s, got %s", user.FirstName, deets.FirstName)
			return
		}

		if deets.LastName != user.LastName {
			t.Errorf("user last name not returned correctly. expected %s, got %s", user.LastName, deets.LastName)
			return
		}

		if deets.Name != user.Name {
			t.Errorf("user name not returned correctly. expected %s, got %s", user.Name, deets.Name)
			return
		}
	}
}

func TestUserUpdate(t *testing.T) {
	testId, err := uuid.NewV4()
	if err != nil {
		t.Logf("error creating new UUID")
	}

	tt := []struct {
		firstName string
		lastName  string
		email     string
		password  string
	}{
		{"first", "last", "first.last." + testId.String() + "@email.com", "secrit_password"},
		{"joey", "joe joe", "j.j" + testId.String() + "@email.com", "joeyjoejoe"},
		{"joey2", "joe joe2", "j.j2" + testId.String() + "@email.com", "joeyjoejoe2"},
	}

	for _, tc := range tt {
		// create the user
		user, err := userFactory(tc.firstName, tc.lastName, tc.email, tc.password)
		if err != nil {
			t.Errorf("user creation failed. Error: %s", err.Error())
			return
		}

		// get the auth token
		auth, err := provide.Authenticate(tc.email, tc.password)
		if err != nil {
			t.Errorf("user authentication failed for user %s. error: %s", tc.email, err.Error())
			return
		}

		// update the user's details
		updatedFirstName := fmt.Sprintf("%s%s", tc.firstName, testId.String())
		updatedLastName := fmt.Sprintf("%s%s", tc.lastName, testId.String())
		updatedName := fmt.Sprintf("%s%s %s%s", tc.firstName, testId.String(), tc.lastName, testId.String())
		updatedEmail := fmt.Sprintf("%s%s", tc.email, testId.String())
		updatedPassword := fmt.Sprintf("%s%s", tc.password, testId.String())

		err = provide.UpdateUser(*auth.Token.Token, user.ID.String(), map[string]interface{}{
			"first_name": updatedFirstName,
			"last_name":  updatedLastName,
			"email":      updatedEmail,
			"password":   updatedPassword,
		})
		if err != nil {
			t.Errorf("error updating user details. Error: %s", err.Error())
		}

		// get the user details
		deets, err := provide.GetUserDetails(*auth.Token.Token, user.ID.String(), map[string]interface{}{})
		if err != nil {
			t.Errorf("error getting details for user id %s. Error: %s", user.ID, err.Error())
			return
		}

		if deets.FirstName != updatedFirstName {
			t.Errorf("user first name not returned correctly. expected %s, got %s", updatedFirstName, deets.FirstName)
			return
		}

		if deets.LastName != updatedLastName {
			t.Errorf("user last name not returned correctly. expected %s, got %s", updatedLastName, deets.LastName)
			return
		}

		if deets.Name != updatedName {
			t.Errorf("user name not returned correctly. expected %s, got %s", updatedName, deets.Name)
			return
		}

		if deets.Email != updatedEmail {
			t.Errorf("user email not returned correctly. expected %s, got %s", updatedEmail, deets.Name)
			return
		}

		//check the updated password
		auth, err = provide.Authenticate(updatedEmail, updatedPassword)
		if err != nil {
			t.Errorf("user authentication failed for user %s. error: %s", updatedEmail, err.Error())
			return
		}

		if auth.Token == nil {
			t.Errorf("no token returned for updated user %s", updatedEmail)
			return
		}
	}
}
