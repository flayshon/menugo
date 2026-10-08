package data

import (
	"fmt"
	"slices"

	"menugo.flayshon.com/internal/validator"
)

// Fulfillment is how an order reaches the customer.
type Fulfillment string

const (
	FulfillmentDelivery Fulfillment = "delivery"
	FulfillmentPickup   Fulfillment = "pickup"
)

func (f Fulfillment) Valid() bool {
	return f == FulfillmentDelivery || f == FulfillmentPickup
}

type OrderStatus string

const (
	StatusPending          OrderStatus = "pending"
	StatusConfirmed        OrderStatus = "confirmed"
	StatusPreparing        OrderStatus = "preparing"
	StatusReadyForDelivery OrderStatus = "ready_for_delivery"
	StatusReadyForPickup   OrderStatus = "ready_for_pickup"
	StatusOutForDelivery   OrderStatus = "out_for_delivery"
	StatusDelivered        OrderStatus = "delivered"
	StatusPickedUp         OrderStatus = "picked_up"
	StatusCancelled        OrderStatus = "cancelled"
)

// AllOrderStatuses lists every status, in lifecycle order.
var AllOrderStatuses = []OrderStatus{
	StatusPending, StatusConfirmed, StatusPreparing, StatusReadyForDelivery, StatusReadyForPickup,
	StatusOutForDelivery, StatusDelivered, StatusPickedUp, StatusCancelled,
}

func (s OrderStatus) Valid() bool {
	return slices.Contains(AllOrderStatuses, s)
}

// orderTransitions lists, for each kind of order, the statuses the
// restaurant may move an order to from each status. Anything not listed is
// forbidden. Final statuses (delivered, picked_up, cancelled) have no exits.
//
//	delivery: pending → confirmed → preparing → ready_for_delivery → out_for_delivery → delivered
//	pickup:   pending → confirmed → preparing → ready_for_pickup → picked_up
//
// The restaurant can cancel at any point until the order has left the
// kitchen: once it's out for delivery, it can't be cancelled.
var orderTransitions = map[Fulfillment]map[OrderStatus][]OrderStatus{
	FulfillmentDelivery: {
		StatusPending:          {StatusConfirmed, StatusCancelled},
		StatusConfirmed:        {StatusPreparing, StatusCancelled},
		StatusPreparing:        {StatusReadyForDelivery, StatusCancelled},
		StatusReadyForDelivery: {StatusOutForDelivery, StatusCancelled},
		StatusOutForDelivery:   {StatusDelivered},
	},
	FulfillmentPickup: {
		StatusPending:        {StatusConfirmed, StatusCancelled},
		StatusConfirmed:      {StatusPreparing, StatusCancelled},
		StatusPreparing:      {StatusReadyForPickup, StatusCancelled},
		StatusReadyForPickup: {StatusPickedUp, StatusCancelled},
	},
}

// ActorType is who changed an order's status.
type ActorType string

const (
	ActorCustomer ActorType = "customer"
	ActorUser     ActorType = "user" // a restaurant member
	ActorSystem   ActorType = "system"
)

// Actor identifies who is changing an order. UserID is set for ActorUser.
type Actor struct {
	Type   ActorType
	UserID int64
}

// CanTransition reports whether actor may move an order of kind f from
// status from to status to. Customers may only cancel orders the restaurant
// hasn't confirmed yet.
func CanTransition(f Fulfillment, from, to OrderStatus, actor ActorType) bool {
	if actor == ActorCustomer {
		return from == StatusPending && to == StatusCancelled
	}
	return slices.Contains(orderTransitions[f][from], to)
}

// NextStatuses returns the statuses the restaurant may move an order of kind
// f to from status s.
func NextStatuses(f Fulfillment, s OrderStatus) []OrderStatus {
	return slices.Clone(orderTransitions[f][s])
}

// InvalidTransitionError is returned for a status change the rules forbid.
type InvalidTransitionError struct {
	From, To OrderStatus
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("an order cannot go from %s to %s", e.From, e.To)
}

// ValidateStatusChange checks a requested status change before the state
// machine is consulted. A reason may only be given when cancelling.
func ValidateStatusChange(v *validator.Validator, to OrderStatus, reason string) {
	v.Check(to != "", "status", "must be provided")
	v.Check(to.Valid(), "status", "is not a valid order status")
	v.Check(validator.MaxChars(reason, 255), "reason", "must not be more than 255 characters long")
	if reason != "" {
		v.Check(to == StatusCancelled, "reason", "can only be given when cancelling")
	}
}
