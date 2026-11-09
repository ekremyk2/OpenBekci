-- +goose Up

-- Sign-up through a site's join code (setup-people S4 path 2): the account is
-- created in the background when the code or link is requested, as 'pending',
-- and becomes 'active' only when the contact proves itself (the SMS code or
-- the e-mailed link). A pending account has no session and cannot sign in by
-- any other means. display_name is the name given at sign-up; the unit claim
-- uses it for the person when the claim does not carry one.
ALTER TABLE "user" DROP CONSTRAINT user_status_check;
ALTER TABLE "user" ADD CONSTRAINT user_status_check CHECK (status IN ('pending', 'active', 'disabled'));
ALTER TABLE "user" ADD COLUMN display_name text CHECK (display_name IS NULL OR (char_length(display_name) BETWEEN 1 AND 200));
-- Abandoned sign-ups are swept by created_at.
CREATE INDEX user_pending_created_at ON "user" (created_at) WHERE status = 'pending';

-- +goose Down
DROP INDEX user_pending_created_at;
ALTER TABLE "user" DROP COLUMN display_name;
DELETE FROM "user" WHERE status = 'pending';
ALTER TABLE "user" DROP CONSTRAINT user_status_check;
ALTER TABLE "user" ADD CONSTRAINT user_status_check CHECK (status IN ('active', 'disabled'));
