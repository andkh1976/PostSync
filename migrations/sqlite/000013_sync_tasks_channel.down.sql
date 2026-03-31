-- SQLite does not support DROP COLUMN in older versions; recreate table
CREATE TABLE sync_tasks_backup AS SELECT id, user_id, status, start_date, end_date, last_synced_id FROM sync_tasks;
DROP TABLE sync_tasks;
ALTER TABLE sync_tasks_backup RENAME TO sync_tasks;
