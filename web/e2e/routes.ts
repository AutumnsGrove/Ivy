// Every screen and designed state, by name. The smoke spec visits each on phone and desktop,
// fails on any console error, and saves a screenshot to web/shots/ for a human to look at.
export const ROUTES: Record<string, string> = {
	'inbox': '/',
	'inbox-empty': '/?scenario=empty',
	'inbox-sync-error': '/?scenario=sync-error',
	'offline': '/?scenario=offline',
	'message': '/m/m1',
	'message-fetch-error': '/m/m1?scenario=fetch-error',
	'message-attachment-error': '/m/m1?scenario=attachment-error'
};
