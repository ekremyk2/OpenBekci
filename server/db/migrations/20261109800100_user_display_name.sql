-- +goose Up

-- The account's own display name (PATCH /v1/me/profile). Null keeps the old behaviour: GET /v1/me shows
-- the name of the person linked to the account, else the email or phone.
ALTER TABLE "user" ADD COLUMN display_name text CHECK (display_name IS NULL OR (display_name = btrim(display_name) AND char_length(display_name) BETWEEN 1 AND 200));

-- +goose Down
ALTER TABLE "user" DROP COLUMN display_name;
