-- +goose Up

-- Append-only by trigger as well as by privilege. The REVOKEs of the audit
-- migration protect against the application roles; a later GRANT ALL ON ALL
-- TABLES or a default-privilege change would silently reopen the tables, and
-- the owner (openbekci_migrator) always holds UPDATE and DELETE. These
-- triggers refuse UPDATE, DELETE and TRUNCATE for every role (a superuser must
-- DROP the trigger deliberately). The function and the triggers are owned by
-- the migrator: the application roles cannot alter or drop them.
-- +goose StatementBegin
CREATE FUNCTION audit_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only: % is not allowed', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_event_append_only_row BEFORE UPDATE OR DELETE ON audit_event
    FOR EACH ROW EXECUTE FUNCTION audit_append_only();
CREATE TRIGGER audit_event_append_only_truncate BEFORE TRUNCATE ON audit_event
    FOR EACH STATEMENT EXECUTE FUNCTION audit_append_only();
CREATE TRIGGER platform_audit_event_append_only_row BEFORE UPDATE OR DELETE ON platform_audit_event
    FOR EACH ROW EXECUTE FUNCTION audit_append_only();
CREATE TRIGGER platform_audit_event_append_only_truncate BEFORE TRUNCATE ON platform_audit_event
    FOR EACH STATEMENT EXECUTE FUNCTION audit_append_only();

-- +goose Down
DROP TRIGGER platform_audit_event_append_only_truncate ON platform_audit_event;
DROP TRIGGER platform_audit_event_append_only_row ON platform_audit_event;
DROP TRIGGER audit_event_append_only_truncate ON audit_event;
DROP TRIGGER audit_event_append_only_row ON audit_event;
DROP FUNCTION audit_append_only();
