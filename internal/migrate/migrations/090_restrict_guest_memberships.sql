CREATE TRIGGER trg_guest_membership_insert
BEFORE INSERT ON project_members
WHEN EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND system_role = 'guest')
 AND (NEW.role <> 'viewer' OR EXISTS (
   SELECT 1 FROM projects WHERE id = NEW.project_id AND expires_at IS NOT NULL
 ))
BEGIN
  SELECT RAISE(ABORT, 'guest role: only regular board viewer memberships are allowed');
END;

CREATE TRIGGER trg_guest_membership_update
BEFORE UPDATE OF project_id, user_id, role ON project_members
WHEN EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND system_role = 'guest')
 AND (NEW.role <> 'viewer' OR EXISTS (
   SELECT 1 FROM projects WHERE id = NEW.project_id AND expires_at IS NOT NULL
 ))
BEGIN
  SELECT RAISE(ABORT, 'guest role: only regular board viewer memberships are allowed');
END;
