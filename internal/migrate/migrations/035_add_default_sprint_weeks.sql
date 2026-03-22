ALTER TABLE projects
ADD COLUMN default_sprint_weeks INTEGER NOT NULL DEFAULT 2
CHECK(default_sprint_weeks IN (1, 2));
