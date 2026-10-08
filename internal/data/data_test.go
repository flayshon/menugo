package data

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"menugo.flayshon.com/internal/testdb"
	"menugo.flayshon.com/internal/validator"
)

func TestRoleCanManage(t *testing.T) {
	tests := []struct {
		actor, target Role
		want          bool
	}{
		{RoleOwner, RoleOwner, false},
		{RoleOwner, RoleAdmin, true},
		{RoleOwner, RoleStaff, true},
		{RoleOwner, RoleDriver, true},
		{RoleAdmin, RoleOwner, false},
		{RoleAdmin, RoleAdmin, false},
		{RoleAdmin, RoleStaff, true},
		{RoleAdmin, RoleDriver, true},
		{RoleStaff, RoleStaff, false},
		{RoleStaff, RoleDriver, false},
		{RoleDriver, RoleDriver, false},
		{Role("bogus"), RoleDriver, false},
	}
	for _, tt := range tests {
		if got := tt.actor.CanManage(tt.target); got != tt.want {
			t.Errorf("%s.CanManage(%s) = %t; want %t", tt.actor, tt.target, got, tt.want)
		}
	}
}

func TestRoleValid(t *testing.T) {
	for _, r := range AnyRole {
		if !r.Valid() {
			t.Errorf("%s should be valid", r)
		}
	}
	for _, r := range []Role{"", "owner", "RESTAURANT_OWNER"} {
		if r.Valid() {
			t.Errorf("%q should not be valid", r)
		}
	}
}

func TestPassword(t *testing.T) {
	var p Password
	if err := p.Set("correct horse"); err != nil {
		t.Fatal(err)
	}
	if string(p.hash) == "correct horse" {
		t.Fatal("password stored in plaintext")
	}

	ok, err := p.Matches("correct horse")
	if err != nil || !ok {
		t.Errorf("Matches(correct) = %t, %v; want true, nil", ok, err)
	}
	ok, err = p.Matches("wrong horse")
	if err != nil || ok {
		t.Errorf("Matches(wrong) = %t, %v; want false, nil", ok, err)
	}
}

func TestValidatePasswordPlaintext(t *testing.T) {
	tests := map[string]bool{
		"":                      false,
		"short":                 false,
		"long enough":           true,
		strings.Repeat("a", 72): true,
		strings.Repeat("a", 73): false,
		// 8 runes but 16 bytes: valid, length is counted in characters.
		"çççççççç": true,
	}
	for pw, want := range tests {
		v := validator.New()
		ValidatePasswordPlaintext(v, pw)
		if v.Valid() != want {
			t.Errorf("ValidatePasswordPlaintext(%q) valid = %t; want %t (%v)", pw, v.Valid(), want, v.Errors)
		}
	}
}

func TestValidateRestaurant(t *testing.T) {
	valid := Restaurant{Name: "Pizza Place", Slug: "pizza-place", Currency: "BRL"}

	tests := []struct {
		name   string
		modify func(*Restaurant)
		field  string
	}{
		{"valid", func(*Restaurant) {}, ""},
		{"blank name", func(r *Restaurant) { r.Name = "  " }, "name"},
		{"long name", func(r *Restaurant) { r.Name = strings.Repeat("x", 201) }, "name"},
		{"short slug", func(r *Restaurant) { r.Slug = "ab" }, "slug"},
		{"bad slug", func(r *Restaurant) { r.Slug = "Pizza Place" }, "slug"},
		{"bad email", func(r *Restaurant) { r.Email = "nope" }, "email"},
		{"empty email is fine", func(r *Restaurant) { r.Email = "" }, ""},
		{"missing currency", func(r *Restaurant) { r.Currency = "" }, "currency"},
		{"unknown currency", func(r *Restaurant) { r.Currency = "XYZ" }, "currency"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := valid
			tt.modify(&r)
			v := validator.New()
			ValidateRestaurant(v, &r)

			if tt.field == "" {
				if !v.Valid() {
					t.Errorf("unexpected errors: %v", v.Errors)
				}
				return
			}
			if _, ok := v.Errors[tt.field]; !ok {
				t.Errorf("expected an error for %q, got %v", tt.field, v.Errors)
			}
		})
	}
}

// insertUser creates a user with a cheap password hash, for tests that
// don't care about the password.
func insertUser(t *testing.T, m Models, email string) *User {
	t.Helper()
	u := &User{Name: "Test User", Email: email, Password: Password{hash: []byte("not-a-real-hash")}}
	if err := m.Users.Insert(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestUserEmailIsUniqueIgnoringCase(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	insertUser(t, m, "alice@example.com")

	err := m.Users.Insert(ctx, &User{Name: "A", Email: "ALICE@example.com", Password: Password{hash: []byte("x")}})
	if !errors.Is(err, ErrDuplicateEmail) {
		t.Errorf("err = %v; want ErrDuplicateEmail", err)
	}

	u, err := m.Users.GetByEmail(ctx, "Alice@Example.com")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "alice@example.com" {
		t.Errorf("email = %q", u.Email)
	}
	if u.CreatedAt.Location() != time.UTC {
		t.Errorf("created_at location = %v; want UTC", u.CreatedAt.Location())
	}
}

func TestTokens(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	u := insertUser(t, m, "alice@example.com")

	token, err := m.Tokens.New(ctx, u.ID, time.Hour, ScopeAuthentication)
	if err != nil {
		t.Fatal(err)
	}
	if len(token.Plaintext) != 26 {
		t.Errorf("token length = %d; want 26", len(token.Plaintext))
	}

	got, err := m.Users.GetForToken(ctx, ScopeAuthentication, token.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID {
		t.Errorf("user ID = %d; want %d", got.ID, u.ID)
	}

	if _, err := m.Users.GetForToken(ctx, "other-scope", token.Plaintext); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("wrong scope: err = %v; want ErrRecordNotFound", err)
	}

	expired, err := m.Tokens.New(ctx, u.ID, -time.Second, ScopeAuthentication)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Users.GetForToken(ctx, ScopeAuthentication, expired.Plaintext); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("expired token: err = %v; want ErrRecordNotFound", err)
	}

	n, err := m.Tokens.DeleteExpired(ctx)
	if err != nil || n != 1 {
		t.Errorf("DeleteExpired = %d, %v; want 1, nil", n, err)
	}

	if err := m.Tokens.Delete(ctx, ScopeAuthentication, token.Plaintext); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Users.GetForToken(ctx, ScopeAuthentication, token.Plaintext); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("deleted token: err = %v; want ErrRecordNotFound", err)
	}
}

func TestRestaurantInsertWithOwnerIsAtomic(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	// The owner doesn't exist, so the membership insert fails; the
	// restaurant must not be left behind without an owner.
	r := &Restaurant{Name: "Orphan", Slug: "orphan", Currency: "BRL"}
	if err := m.Restaurants.InsertWithOwner(ctx, r, 999999); err == nil {
		t.Fatal("expected an error")
	}

	var count int
	if err := m.Restaurants.DB.QueryRow("SELECT COUNT(*) FROM restaurants").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("restaurants = %d; want 0", count)
	}

	owner := insertUser(t, m, "owner@example.com")
	r = &Restaurant{Name: "Pizza", Slug: "pizza", Currency: "BRL"}
	if err := m.Restaurants.InsertWithOwner(ctx, r, owner.ID); err != nil {
		t.Fatal(err)
	}

	ms, err := m.Memberships.Get(ctx, r.ID, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ms.Role != RoleOwner {
		t.Errorf("role = %s; want %s", ms.Role, RoleOwner)
	}

	dup := &Restaurant{Name: "Other", Slug: "pizza", Currency: "BRL"}
	if err := m.Restaurants.InsertWithOwner(ctx, dup, owner.ID); !errors.Is(err, ErrDuplicateSlug) {
		t.Errorf("err = %v; want ErrDuplicateSlug", err)
	}
}

func TestRestaurantUpdateDetectsConflicts(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	owner := insertUser(t, m, "owner@example.com")
	r := &Restaurant{Name: "Pizza", Slug: "pizza", Currency: "BRL"}
	if err := m.Restaurants.InsertWithOwner(ctx, r, owner.ID); err != nil {
		t.Fatal(err)
	}

	first, _ := m.Restaurants.Get(ctx, r.ID)
	second, _ := m.Restaurants.Get(ctx, r.ID)

	first.Name = "Pizza 1"
	if err := m.Restaurants.Update(ctx, first); err != nil {
		t.Fatal(err)
	}
	if first.Version != 2 {
		t.Errorf("version = %d; want 2", first.Version)
	}

	second.Name = "Pizza 2"
	if err := m.Restaurants.Update(ctx, second); !errors.Is(err, ErrEditConflict) {
		t.Errorf("err = %v; want ErrEditConflict", err)
	}

	got, _ := m.Restaurants.Get(ctx, r.ID)
	if got.Name != "Pizza 1" || !got.UpdatedAt.Equal(first.UpdatedAt) {
		t.Errorf("got %q updated %v; want %q updated %v", got.Name, got.UpdatedAt, "Pizza 1", first.UpdatedAt)
	}
}

func TestRestaurantListForUserOnlyReturnsOwnRestaurants(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	alice := insertUser(t, m, "alice@example.com")
	bob := insertUser(t, m, "bob@example.com")

	for _, slug := range []string{"alice-one", "alice-two"} {
		if err := m.Restaurants.InsertWithOwner(ctx, &Restaurant{Name: slug, Slug: slug, Currency: "BRL"}, alice.ID); err != nil {
			t.Fatal(err)
		}
	}
	bobs := &Restaurant{Name: "bob", Slug: "bob", Currency: "BRL"}
	if err := m.Restaurants.InsertWithOwner(ctx, bobs, bob.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Memberships.Insert(ctx, &Membership{RestaurantID: bobs.ID, UserID: alice.ID, Role: RoleStaff}); err != nil {
		t.Fatal(err)
	}

	list, err := m.Restaurants.ListForUser(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range list {
		got = append(got, r.Slug+":"+string(r.Role))
	}
	want := "alice-one:restaurant_owner,alice-two:restaurant_owner,bob:restaurant_staff"
	if strings.Join(got, ",") != want {
		t.Errorf("got %v; want %s", got, want)
	}

	list, err = m.Restaurants.ListForUser(ctx, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Slug != "bob" {
		t.Errorf("bob's restaurants = %+v", list)
	}
}

func TestMemberships(t *testing.T) {
	t.Parallel()
	m := NewModels(testdb.New(t))
	ctx := context.Background()

	owner := insertUser(t, m, "owner@example.com")
	staff := insertUser(t, m, "staff@example.com")
	r := &Restaurant{Name: "Pizza", Slug: "pizza", Currency: "BRL"}
	if err := m.Restaurants.InsertWithOwner(ctx, r, owner.ID); err != nil {
		t.Fatal(err)
	}

	ms := &Membership{RestaurantID: r.ID, UserID: staff.ID, Role: RoleStaff}
	if err := m.Memberships.Insert(ctx, ms); err != nil {
		t.Fatal(err)
	}
	if err := m.Memberships.Insert(ctx, &Membership{RestaurantID: r.ID, UserID: staff.ID, Role: RoleAdmin}); !errors.Is(err, ErrDuplicateMembership) {
		t.Errorf("err = %v; want ErrDuplicateMembership", err)
	}

	members, err := m.Memberships.ListMembers(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || members[1].Email != "staff@example.com" {
		t.Errorf("members = %+v", members)
	}

	// Deleting with a stale role must not remove the membership.
	if err := m.Memberships.Delete(ctx, r.ID, staff.ID, RoleAdmin); !errors.Is(err, ErrEditConflict) {
		t.Errorf("err = %v; want ErrEditConflict", err)
	}
	if err := m.Memberships.Delete(ctx, r.ID, staff.ID, RoleStaff); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Memberships.Get(ctx, r.ID, staff.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("err = %v; want ErrRecordNotFound", err)
	}

	// Deleting the restaurant cascades to its memberships.
	if err := m.Restaurants.Delete(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Memberships.Get(ctx, r.ID, owner.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("err = %v; want ErrRecordNotFound", err)
	}
	if err := m.Restaurants.Delete(ctx, r.ID); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("second delete: err = %v; want ErrRecordNotFound", err)
	}
}
