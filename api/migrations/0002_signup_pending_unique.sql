-- At most one unconsumed (pending or approved) signup request per email.
-- A consumed request no longer blocks a future request for the same address.
CREATE UNIQUE INDEX signup_requests_pending_email
    ON signup_requests(email) WHERE consumed_at IS NULL;
