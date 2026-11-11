-- message_events_mv v2: also the app inbox (inbox_sent, inbox_opened, inbox_clicked) and email
-- bounces. A materialized view's query can't be changed in place, so v1 is replaced.
DROP VIEW IF EXISTS message_events_mv;

-- v2 (inbox and email bounces) is replaced by message_events_mv3 in 006_delivery_and_dashboards.sql.
