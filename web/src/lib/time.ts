// Message times are instants on the wire (RFC 3339, UTC) and become "9:41",
// "Yesterday", "Wed" or "Sep 26" here, in the viewer's own timezone and locale.
// The server cannot do this: it does not know the viewer's zone (a container's
// is UTC), so a time rendered there is wrong on the phone (N15, round 32b).

export type TimeOptions = {
	/** The moment "today" is measured from; tests pin it. */
	now?: Date;
	/** A BCP 47 tag; undefined follows the browser. */
	locale?: string;
	/** An IANA zone; undefined follows the browser. */
	timeZone?: string;
};

// Building an Intl formatter is the expensive part, and a list formats dozens of
// rows, so each distinct (kind, locale, zone, options) is built once.
const cache = new Map<string, Intl.DateTimeFormat | Intl.RelativeTimeFormat>();

function dateFormat(locale: string | undefined, timeZone: string | undefined, o: Intl.DateTimeFormatOptions) {
	const key = `d|${locale}|${timeZone}|${JSON.stringify(o)}`;
	let f = cache.get(key) as Intl.DateTimeFormat | undefined;
	if (!f) {
		f = new Intl.DateTimeFormat(locale, { ...o, timeZone });
		cache.set(key, f);
	}
	return f;
}

function relativeFormat(locale: string | undefined) {
	const key = `r|${locale}`;
	let f = cache.get(key) as Intl.RelativeTimeFormat | undefined;
	if (!f) {
		f = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
		cache.set(key, f);
	}
	return f;
}

/** The viewer's calendar day for an instant, as a day number plus its year. */
function calendarDay(d: Date, timeZone: string | undefined): { day: number; year: number } {
	// en-CA renders YYYY-MM-DD whatever the viewer's locale, in the requested zone.
	const key = dateFormat('en-CA', timeZone, { year: 'numeric', month: '2-digit', day: '2-digit' }).format(d);
	const [year, month, dom] = key.split('-').map(Number);
	return { day: Date.UTC(year, month - 1, dom) / 86_400_000, year };
}

/**
 * Formats a message's instant for a list row or a header: the clock time for
 * today, "Yesterday", a weekday within the last week, then a month and day (with
 * the year when it is not this one). An unknown instant (empty, unparseable, or
 * the zero instant a message with no Date header carries) is the empty string.
 */
export function formatMessageTime(iso: string, opts: TimeOptions = {}): string {
	const when = new Date(iso);
	// 1971 is well clear of the zero instant (year 1) and of any real mail.
	if (Number.isNaN(when.getTime()) || when.getUTCFullYear() < 1971) return '';

	const { now = new Date(), locale, timeZone } = opts;
	const then = calendarDay(when, timeZone);
	const today = calendarDay(now, timeZone);
	const ago = today.day - then.day;

	if (ago === 0) {
		return dateFormat(locale, timeZone, { hour: 'numeric', minute: '2-digit' }).format(when);
	}
	if (ago === 1) {
		const word = relativeFormat(locale).format(-1, 'day');
		return word.charAt(0).toLocaleUpperCase(locale) + word.slice(1);
	}
	if (ago > 1 && ago < 7) {
		return dateFormat(locale, timeZone, { weekday: 'short' }).format(when);
	}
	if (then.year === today.year) {
		return dateFormat(locale, timeZone, { month: 'short', day: 'numeric' }).format(when);
	}
	return dateFormat(locale, timeZone, { month: 'short', day: 'numeric', year: 'numeric' }).format(when);
}

/**
 * How long ago an instant was, for "synced 4 minutes ago": relative up to a
 * month, in the viewer's language ("vor 4 Minuten", "yesterday"), then a date.
 * An instant in the near future is clock skew and reads as now; an unknown one
 * is the empty string.
 */
export function formatSince(iso: string, opts: TimeOptions = {}): string {
	const when = new Date(iso);
	if (Number.isNaN(when.getTime()) || when.getUTCFullYear() < 1971) return '';

	const { now = new Date(), locale, timeZone } = opts;
	const seconds = Math.round((now.getTime() - when.getTime()) / 1000);
	const relative = relativeFormat(locale);
	if (seconds < 45) return relative.format(0, 'second');
	if (seconds < 3600) return relative.format(-Math.round(seconds / 60), 'minute');
	if (seconds < 86_400) return relative.format(-Math.round(seconds / 3600), 'hour');
	const days = Math.round(seconds / 86_400);
	if (days < 30) return relative.format(-days, 'day');

	const sameYear = calendarDay(when, timeZone).year === calendarDay(now, timeZone).year;
	return dateFormat(locale, timeZone, {
		month: 'short',
		day: 'numeric',
		...(sameYear ? {} : { year: 'numeric' })
	}).format(when);
}
