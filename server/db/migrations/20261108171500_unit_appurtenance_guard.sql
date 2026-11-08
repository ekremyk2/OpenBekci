-- +goose Up

-- Appurtenances (S2) form one level: a unit with a parent has no children and a
-- parent has no parent. The application checks this for a clear message, but two
-- concurrent updates can each pass that check and together build a chain or a
-- cycle; this trigger closes the gap. The BEFORE ROW trigger runs after the
-- updated row is locked, and the parent row is locked here, so concurrent
-- changes serialize on it.
-- +goose StatementBegin
CREATE FUNCTION unit_appurtenance_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    grandparent uuid;
BEGIN
    IF NEW.parent_unit_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT parent_unit_id INTO grandparent FROM unit
        WHERE org_id = NEW.org_id AND id = NEW.parent_unit_id FOR UPDATE;
    IF grandparent IS NOT NULL THEN
        RAISE EXCEPTION 'appurtenance chain: unit % is itself an appurtenance', NEW.parent_unit_id USING ERRCODE = '23514';
    END IF;
    IF EXISTS (SELECT 1 FROM unit WHERE org_id = NEW.org_id AND parent_unit_id = NEW.id) THEN
        RAISE EXCEPTION 'appurtenance chain: unit % already has appurtenances', NEW.id USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER unit_appurtenance_guard BEFORE INSERT OR UPDATE OF parent_unit_id ON unit
    FOR EACH ROW EXECUTE FUNCTION unit_appurtenance_guard();

-- +goose Down
DROP TRIGGER unit_appurtenance_guard ON unit;
DROP FUNCTION unit_appurtenance_guard();
