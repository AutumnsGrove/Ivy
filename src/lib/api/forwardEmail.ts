/**
 * Forward Email API Client
 *
 * Handles communication with Forward Email for SMTP/IMAP operations.
 * https://forwardemail.net/en/email-api
 */

export interface ForwardEmailConfig {
	apiKey: string;
	baseUrl?: string;
}

export interface SendEmailParams {
	from: string;
	to: string[];
	cc?: string[];
	bcc?: string[];
	subject: string;
	html?: string;
	text?: string;
	attachments?: Array<{
		filename: string;
		content: ArrayBuffer;
		contentType: string;
	}>;
}

export class ForwardEmailClient {
	private apiKey: string;
	private baseUrl: string;

	constructor(config: ForwardEmailConfig) {
		this.apiKey = config.apiKey;
		this.baseUrl = config.baseUrl ?? 'https://api.forwardemail.net/v1';
	}

	// TODO: Implement send email
	async sendEmail(params: SendEmailParams): Promise<{ messageId: string }> {
		throw new Error('Not implemented');
	}

	// TODO: Implement webhook signature verification
	static verifyWebhookSignature(
		payload: string,
		signature: string,
		secret: string
	): Promise<boolean> {
		throw new Error('Not implemented');
	}
}
