// Seed data lifted from docs/design/canvas/project so every screen renders the way it was designed.
// Replaced by the real backend; nothing outside api/ imports this file.
import type {
	Account,
	AskAnswer,
	Check,
	CheckDetail,
	MailMessage,
	MailSummary,
	Person,
	ReadingFeed,
	Rule,
	SearchHit,
	TagsOverview
} from '../types';

export const accounts: Account[] = [
	{
		id: 'a1',
		address: 'me@example.com',
		short: 'me@',
		initial: 'A',
		name: '',
		icon: '',
		photo: false,
		slot: 1,
		unread: 9,
		smart: true,
		sync: 'ok',
		syncNote: 'Up to date · synced just now'
	},
	{
		id: 'a2',
		address: 'hello@example.com',
		short: 'hello@',
		initial: 'H',
		name: '',
		icon: '',
		photo: false,
		slot: 2,
		unread: 5,
		smart: true,
		sync: 'ok',
		syncNote: 'Up to date · synced just now'
	},
	{
		id: 'a3',
		address: 'support@example.com',
		short: 'support@',
		initial: 'D',
		name: '',
		icon: '',
		photo: false,
		slot: 3,
		unread: 1,
		smart: false,
		sync: 'ok',
		syncNote: 'Up to date · synced 4 min ago'
	},
	{
		id: 'a4',
		address: 'alerts@example.com',
		short: 'alerts@',
		initial: 'S',
		name: '',
		icon: '',
		photo: false,
		slot: 4,
		unread: 1,
		smart: false,
		sync: 'ok',
		syncNote: 'Up to date · synced 2 min ago'
	}
];

/** hello@ can't sign in; used by the sync-error scenario and the mirror health screen. */
export const failingHello = (a: Account): Account =>
	a.id === 'a2'
		? { ...a, sync: 'auth-failed', syncNote: "Can't sign in · last synced 14 min ago" }
		: a;

/** Mirror health shows the whole spread: healthy, failing, and still backfilling. */
export const healthAccounts: Account[] = accounts.map((a) =>
	a.id === 'a3'
		? {
				...a,
				sync: 'syncing' as const,
				syncNote: 'Reading your mailbox, newest first',
				progress: 0.62
			}
		: failingHello(a)
);

// The API sends instants, so the designed rows ("9:41", "Yesterday", "Mon") are
// built relative to now in the viewer's own zone and the formatter does the rest.
const ago = (days: number, hour = 9, minute = 0): string => {
	const d = new Date();
	d.setDate(d.getDate() - days);
	d.setHours(hour, minute, 0, 0);
	return d.toISOString();
};

const mara = {
	from: 'Mara Linden',
	initials: 'ML'
};

export const inbox: MailSummary[] = [
	{
		id: 'm1',
		accountId: 'a2',
		...mara,
		date: ago(0, 9, 41),
		subject: 'Moving my blog over to Grove?',
		preview: 'Hi! A friend pointed me to Grove and I wondered whether I can bring my old posts…',
		unread: true,
		needs: true,
		tag: 'contact form'
	},
	{
		id: 'm2',
		accountId: 'a3',
		from: 'Takedown requests',
		initials: 'TR',
		date: ago(1, 16, 20),
		subject: 'Notice of alleged infringement',
		preview: 'We are writing on behalf of a rights holder to notify you of material hosted at…',
		unread: true,
		needs: true,
		tag: 'legal'
	},
	{
		id: 'm3',
		accountId: 'a1',
		from: 'GitHub',
		initials: 'GH',
		date: ago(0, 8, 12),
		subject: '[Lattice] Pull request merged into main',
		preview: 'autumnsgrove merged 3 commits. Review the changes and the follow-up checks…',
		unread: false,
		needs: false
	},
	{
		id: 'm4',
		accountId: 'a1',
		from: 'Purelymail',
		initials: 'PM',
		date: ago(3, 10, 2),
		subject: 'Your receipt for this month',
		preview: 'Thank you for your payment. This receipt covers your account and its users…',
		unread: false,
		needs: false,
		tag: 'receipt'
	},
	{
		id: 'm5',
		accountId: 'a1',
		from: 'Wildflower Weekly',
		initials: 'WW',
		date: ago(3, 7, 30),
		subject: 'Ten shade plants that forgive neglect',
		preview: 'This week in the garden: ferns, hostas, and the quiet case for moss…',
		unread: false,
		needs: false
	}
];

const maraBody: Omit<MailMessage, keyof MailSummary> = {
	toShort: 'hello@',
	toFull: 'hello@example.com',
	summary: 'Mara wonders if she can bring her old posts and images to Grove. No rush.',
	html: '<p>Hi there,</p><p>I found Grove through a friend, and I&#39;ve been writing on a small blog for about six years. Before I set anything up, I wanted to ask whether I can bring my old posts with me, and whether the images come along too.</p><p>No rush at all. Thank you for building something so gentle.</p><img src="/favicon.ico" alt="inline logo" width="32" height="32">',
	paragraphs: [
		'Hi there,',
		"I found Grove through a friend, and I've been writing on a small blog for about six years. Before I set anything up, I wanted to ask whether I can bring my old posts with me, and whether the images come along too.",
		'No rush at all. Thank you for building something so gentle.'
	],
	attachments: [
		{ id: 'f1', name: 'blog-home.png', size: '1.1 MB', kind: 'image', tone: 'a' },
		{ id: 'f2', name: 'post-photo.jpg', size: '640 KB', kind: 'image', tone: 'b' },
		{ id: 'f3', name: 'blog-export.zip', size: '2.4 MB', kind: 'file' }
	]
};

export function messageBody(id: string): Omit<MailMessage, keyof MailSummary> {
	if (id === 'm1') return maraBody;
	const m = inbox.find((x) => x.id === id);
	return {
		toShort: 'me@',
		toFull: 'me@example.com',
		html: `<p>${m?.preview.replace(/…$/, '.') ?? ''}</p><p>The rest of this message is a placeholder.</p>`,
		paragraphs: [m?.preview.replace(/…$/, '.') ?? '', 'The rest of this message is a placeholder.'],
		attachments: []
	};
}

export const reading: ReadingFeed = {
	digest:
		'Five issues today, mostly gardening. One long read on moss is worth saving for tonight.',
	issues: [
		{
			id: 'i1',
			sender: 'Wildflower Weekly',
			initials: 'WW',
			minutes: 7,
			read: false,
			title: 'The quiet case for moss',
			blurb: 'Why the softest thing in the garden is also the toughest, and how to invite it in.'
		},
		{
			id: 'i2',
			sender: 'Fern & Field',
			initials: 'FF',
			minutes: 3,
			read: false,
			title: "What to do with October's last tomatoes",
			blurb: 'Green ones, split ones, and the ones the squirrels left behind.'
		},
		{
			id: 'i3',
			sender: 'The Compost Letter',
			initials: 'TC',
			read: true,
			title: 'Turning the pile, in four seasons'
		}
	]
};

export const searchCorpus: SearchHit[] = [
	{
		id: 's1',
		accountId: 'a1',
		from: 'Cloudflare',
		date: ago(4, 13, 5),
		subject: 'Your domain renews soon',
		preview:
			'grove.place will renew on the 14th. No action is needed unless you want to change the domain settings…'
	},
	{
		id: 's2',
		accountId: 'a1',
		from: 'Cloudflare',
		date: ago(365, 11, 15),
		subject: 'Receipt for your renewal',
		preview: 'Thanks for your payment. This receipt is for the domain grove.place…',
		hasAttachment: true,
		tag: 'receipt'
	},
	{
		id: 's3',
		accountId: 'a1',
		from: 'Namebase',
		date: ago(200, 9, 0),
		subject: 'Your registration is expiring',
		preview: 'Keep your name by extending it before the end of the month…',
		semantic: true
	}
];

export const askAnswer: AskAnswer = {
	question: 'When does my domain renew, and what did I pay last time?',
	steps: [
		{ kind: 'search', text: 'Searched for “domain renewal”' },
		{ kind: 'read', text: 'Read “Your domain renews soon”' },
		{ kind: 'read', text: 'Read “Receipt for your renewal”' },
		{ kind: 'think', text: "Comparing this year's price with last year's" }
	],
	answer: [
		"Your domain renews on the 14th [1]. Last year's receipt [2] shows what you paid, and the new notice says the price is unchanged."
	],
	sources: [
		{ n: 1, subject: 'Your domain renews soon', meta: 'Cloudflare · me@ · Tue' },
		{ n: 2, subject: 'Receipt for your renewal', meta: 'Cloudflare · me@ · last year' }
	]
};

export const tags: TagsOverview = {
	mine: [
		{ id: 't1', name: 'receipts', color: 'sky', count: 24 },
		{ id: 't2', name: 'legal', color: 'coral', count: 3 },
		{ id: 't3', name: 'contact form', color: 'rose', count: 11 },
		{ id: 't4', name: 'grove', color: 'teal', count: 7 },
		{ id: 't5', name: 'ideas', color: 'lilac', count: 2 }
	],
	placed: [
		{ id: 'p1', name: 'needs you', count: 2 },
		{ id: 'p2', name: 'newsletters', count: 14 },
		{ id: 'p3', name: 'looks real, found in Junk', count: 1 }
	],
	activeRules: 3
};

export const people: Person[] = [
	{
		id: 'p-ml',
		name: 'Mara Linden',
		initials: 'ML',
		email: 'mara@example.com',
		slot: 2,
		latest: 'Moving my blog over to Grove?',
		when: '9:41',
		writesTo: 'hello@',
		since: 'March',
		tags: ['contact form'],
		conversations: [
			{
				id: 'm1',
				subject: 'Moving my blog over to Grove?',
				preview: 'Hi! A friend pointed me to Grove and I wondered whether…',
				when: '9:41',
				unread: true
			},
			{
				id: 'c2',
				subject: 'Question about custom domains',
				preview: 'You: Happy to help. Here is how that works…',
				when: 'Mar'
			}
		]
	},
	{
		id: 'p-eb',
		name: 'Eli Brandt',
		initials: 'EB',
		email: 'eli@example.com',
		slot: 1,
		latest: 'Re: the garden party',
		when: 'Mon',
		writesTo: 'me@',
		since: 'June',
		tags: [],
		conversations: []
	},
	{
		id: 'p-jo',
		name: 'Jo Okafor',
		initials: 'JO',
		email: 'jo@example.com',
		slot: 1,
		latest: 'Thanks for the seedlings',
		when: 'Sun',
		writesTo: 'me@',
		since: 'April',
		tags: [],
		conversations: []
	},
	{
		id: 'p-ps',
		name: 'Priya Shah',
		initials: 'PS',
		email: 'priya@example.com',
		slot: 5,
		latest: 'Notes from Thursday',
		when: 'Oct 2',
		writesTo: 'me@',
		since: 'January',
		tags: [],
		conversations: []
	},
	{
		id: 'p-tk',
		name: 'Taro Kimura',
		initials: 'TK',
		email: 'taro@example.com',
		slot: 3,
		latest: 'Photos from the greenhouse',
		when: 'Sep 28',
		writesTo: 'me@',
		since: 'May',
		tags: [],
		conversations: []
	}
];

export const rules: Rule[] = [
	{
		id: 'r1',
		when: 'mail is from',
		whenToken: 'Cloudflare',
		whenTail: 'and looks like a receipt',
		then: 'tag it',
		thenToken: 'receipts',
		thenColor: 'sky',
		matches: 12,
		on: true
	},
	{
		id: 'r2',
		when: 'mail looks like a job application',
		then: 'tag it',
		thenToken: 'jobs',
		thenColor: 'gold',
		matches: 3,
		on: true
	},
	{
		id: 'r3',
		when: 'mail looks like a newsletter',
		then: 'show it in',
		thenToken: 'Reading',
		matches: 14,
		on: true
	},
	{
		id: 'r4',
		when: 'mail has an attachment from',
		whenToken: 'Mara Linden',
		then: 'tag it',
		thenToken: 'contact form',
		thenColor: 'rose',
		matches: 0,
		on: false
	}
];

export const checks: Check[] = [
	{
		id: 'k1',
		name: 'Looks like a receipt',
		description: 'A payment confirmation, invoice or renewal charge',
		builtIn: false,
		on: true
	},
	{
		id: 'k2',
		name: 'A job application',
		description: 'Someone applying or asking about work with you',
		builtIn: false,
		on: true
	},
	{
		id: 'k3',
		name: 'Needs you',
		description: 'Something waiting on a reply or action',
		builtIn: true,
		on: true
	},
	{
		id: 'k4',
		name: 'Newsletter',
		description: 'Regular issues you chose to receive',
		builtIn: true,
		on: true
	},
	{
		id: 'k5',
		name: 'Contact form',
		description: 'Messages sent through a form on your site',
		builtIn: true,
		on: true
	},
	{
		id: 'k6',
		name: 'Has a deadline',
		description: 'A date something must happen by',
		builtIn: true,
		on: false
	}
];

export const checkDetail: CheckDetail = {
	...checks[0],
	prompt:
		'A confirmation of a payment: a receipt, an invoice, or a renewal charge. Not announcements, offers, or marketing.',
	sureness: 'balanced',
	runsOn: ['a1', 'a2'],
	usedBy: 'From Cloudflare, tag receipts'
};
