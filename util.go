package turnpike

import (
	"math/rand"
	"time"
)

const (
	// --- Interactions ---

	// Peer provided an incorrect URI for any URI-based attribute of WAMP message,
	// such as realm, topic or procedure.
	WAMP_ERROR_INVALID_URI = URI("wamp.error.invalid_uri")

	// A Dealer could not perform a call, since no procedure is currently
	// registered under the given URI.
	WAMP_ERROR_NO_SUCH_PROCEDURE = URI("wamp.error.no_such_procedure")

	// A procedure could not be registered, since a procedure with the given URI is already registered.
	WAMP_ERROR_PROCEDURE_ALREADY_EXISTS = URI("wamp.error.procedure_already_exists")

	// A Dealer could not perform an unregister, since the given registration is not active.
	WAMP_ERROR_NO_SUCH_REGISTRATION = URI("wamp.error.no_such_registration")

	// A Broker could not perform an unsubscribe, since the given subscription is not active.
	WAMP_ERROR_NO_SUCH_SUBSCRIPTION = URI("wamp.error.no_such_subscription")

	// A call failed, since the given argument types or values are not acceptable to the called
	// procedure - in which case the Callee may throw this error. Or a Router performing payload
	// validation checked the payload (args / kwargs) of a call, call result, call error or publish,
	// and the payload did not conform - in which case the Router may throw this error.
	WAMP_ERROR_INVALID_ARGUMENT = URI("wamp.error.invalid_argument")

	// --- Session Close ---

	// The Peer is shutting down completely - used as a GOODBYE (or ABORT) reason.
	WAMP_ERROR_SYSTEM_SHUTDOWN = URI("wamp.error.system_shutdown")

	// The Peer wants to leave the realm - used as a GOODBYE reason.
	WAMP_ERROR_CLOSE_REALM = URI("wamp.error.close_realm")

	// A Peer acknowledges ending of a session - used as a GOOBYE reply reason.
	WAMP_ERROR_GOODBYE_AND_OUT = URI("wamp.error.goodbye_and_out")

	// --- Authorization ---

	// A join, call, register, publish or subscribe failed, since the Peer is not authorized to
	// perform the operation.
	WAMP_ERROR_NOT_AUTHORIZED = URI("wamp.error.not_authorized")

	// A Dealer or Broker could not determine if the Peer is authorized to perform a join, call,
	// register, publish or subscribe, since the authorization operation itself failed. E.g. a custom
	// authorizer ran into an error.
	WAMP_ERROR_AUTHORIZATION_FAILED = URI("wamp.error.authorization_failed")

	// Peer wanted to join a non-existing realm (and the Router did not allow to auto-create the realm)
	WAMP_ERROR_NO_SUCH_REALM = URI("wamp.error.no_such_realm")

	// A Peer was to be authenticated under a Role that does not (or no longer) exists on the Router.
	// For example, the Peer was successfully authenticated, but the Role configured does not exists -
	// hence there is some misconfiguration in the Router.
	WAMP_ERROR_NO_SUCH_ROLE = URI("wamp.error.no_such_role")
)

const (
	MAX_ID = 1 << 53
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func NewID() ID {
	return ID(rand.Intn(MAX_ID))
}