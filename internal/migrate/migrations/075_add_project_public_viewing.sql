ALTER TABLE projects
ADD COLUMN public_view_enabled INTEGER NOT NULL DEFAULT 0
CHECK(public_view_enabled IN (0, 1));
