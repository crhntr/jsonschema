-- name: ListIssues :many
SELECT issues.id, issues.title, issues.severity, issues.status, issues.assignee,
       contacts.id AS assignee_id, contacts.name AS assignee_name, issues.updated_at
FROM issues
LEFT JOIN contacts ON contacts.email = issues.assignee
ORDER BY CASE issues.status WHEN 'open' THEN 0 WHEN 'in progress' THEN 1 ELSE 2 END,
         issues.id DESC;

-- name: GetIssue :one
SELECT * FROM issues WHERE id = ?;

-- name: CreateIssue :one
INSERT INTO issues (title, severity, status, assignee, body)
VALUES (?, ?, ?, ?, ?)
RETURNING id;

-- name: UpdateIssue :exec
UPDATE issues
SET title = ?, severity = ?, status = ?, assignee = ?, body = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteIssue :execrows
DELETE FROM issues WHERE id = ?;

-- name: CountIssuesByStatus :many
SELECT status, COUNT(*) AS count FROM issues GROUP BY status ORDER BY status;

-- name: ListContacts :many
SELECT contacts.id, contacts.name, contacts.email, contacts.role,
       (SELECT COUNT(*) FROM issues
        WHERE issues.assignee = contacts.email AND issues.status != 'closed') AS open_issues
FROM contacts
ORDER BY contacts.name;

-- name: GetContact :one
SELECT * FROM contacts WHERE id = ?;

-- name: GetContactByEmail :one
SELECT * FROM contacts WHERE email = ?;

-- name: CreateContact :one
INSERT INTO contacts (name, email, role, body)
VALUES (?, ?, ?, ?)
RETURNING id;

-- name: UpdateContact :exec
UPDATE contacts SET name = ?, email = ?, role = ?, body = ? WHERE id = ?;

-- name: DeleteContact :execrows
DELETE FROM contacts WHERE id = ?;

-- name: ListIssuesForAssignee :many
SELECT id, title, severity, status FROM issues WHERE assignee = ? ORDER BY id DESC;

-- name: GetSettings :one
SELECT * FROM settings WHERE id = 1;

-- name: SaveSettings :exec
INSERT INTO settings (id, project, body) VALUES (1, ?, ?)
ON CONFLICT (id) DO UPDATE SET project = excluded.project, body = excluded.body;
