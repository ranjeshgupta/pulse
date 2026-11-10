-- Message decisions: an event wait can be for one specific message (journey send step), e.g.
-- "this email was opened". Empty matches the event from any message.
ALTER TABLE journey_event_waits ADD COLUMN message_id text NOT NULL DEFAULT '';
