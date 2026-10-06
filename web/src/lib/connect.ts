import { ApiError } from './api/errors';

/**
 * What the connect and update-password screens say when a try fails. The words are
 * ours, never the server's message: that text could name a host, and "wrong
 * password" and "provider down" need different advice.
 */
export function connectErrorText(e: unknown): string {
	if (!(e instanceof ApiError)) return 'Something went wrong. Try again.';
	switch (e.code) {
		case 'auth_failed':
			return "Purelymail didn't accept that email address and password. Check them and try again. Nothing was saved.";
		case 'unreachable':
			return "Couldn't reach Purelymail. Check the connection and try again. Nothing was saved.";
		case 'connect_failed':
			return "Couldn't sign in to Purelymail. Try again in a moment. Nothing was saved.";
		case 'already_connected':
			return 'That address is already connected.';
		case 'connect_unavailable':
			return "This copy of Ivy can't connect accounts from the app.";
		case 'offline':
			return "Can't reach Ivy. Check that you're on your tailnet and try again.";
		case 'bad_request':
			return 'Enter your email address and password.';
		default:
			return 'Something went wrong. Try again.';
	}
}
