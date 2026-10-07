// Account photos are shrunk in the browser before they are uploaded: a phone photo is several
// megabytes (and HEIC, which the server refuses) but the badge never shows more than a small
// square. Safari decodes HEIC itself, so decoding here is what makes iPhone photos work at all.
// The server's 5 MiB limit and format sniff stay as the backstop.

/** The longest edge of the stored square, in image pixels (not CSS). */
export const PHOTO_EDGE = 512;

/** The browser could not turn the file into a photo; the message is safe to show. */
export class PhotoError extends Error {
	constructor(message: string) {
		super(message);
		this.name = 'PhotoError';
	}
}

export type Crop = { sx: number; sy: number; side: number };
type Decoded = { width: number; height: number; close?: () => void };

/** The browser pieces, injected so the logic runs without a canvas. */
export type PhotoDeps = {
	decode: (file: Blob) => Promise<Decoded>;
	encode: (img: Decoded, crop: Crop, edge: number, type: 'image/jpeg' | 'image/png') => Promise<Blob | null>;
};

/** The largest centred square inside a width by height image. */
export function squareCrop(width: number, height: number): Crop {
	const side = Math.min(width, height);
	return { sx: Math.floor((width - side) / 2), sy: Math.floor((height - side) / 2), side };
}

// Formats that can carry transparency stay PNG so a logo does not gain a black background;
// everything else is a photo and becomes a small JPEG.
const KEEPS_ALPHA = new Set(['image/png', 'image/gif', 'image/webp']);

export async function squarePhoto(file: Blob, deps: PhotoDeps = browserDeps): Promise<Blob> {
	let img: Decoded;
	try {
		img = await deps.decode(file);
	} catch {
		throw new PhotoError("This browser can't read that photo. Try a JPEG or PNG.");
	}
	try {
		if (!(img.width > 0 && img.height > 0)) throw new PhotoError("That image has nothing in it.");
		const crop = squareCrop(img.width, img.height);
		const out = await deps.encode(img, crop, Math.min(crop.side, PHOTO_EDGE), KEEPS_ALPHA.has(file.type) ? 'image/png' : 'image/jpeg');
		if (!out) throw new PhotoError("That photo couldn't be resized.");
		return out;
	} finally {
		img.close?.();
	}
}

const browserDeps: PhotoDeps = {
	// `from-image` applies the EXIF rotation, so a portrait iPhone photo is not drawn sideways.
	decode: (file) => createImageBitmap(file, { imageOrientation: 'from-image' }),
	encode(img, { sx, sy, side }, edge, type) {
		const canvas = document.createElement('canvas');
		canvas.width = canvas.height = edge;
		const ctx = canvas.getContext('2d');
		if (!ctx) return Promise.resolve(null);
		ctx.imageSmoothingQuality = 'high';
		ctx.drawImage(img as ImageBitmap, sx, sy, side, side, 0, 0, edge, edge);
		return new Promise((resolve) => canvas.toBlob(resolve, type, 0.85));
	}
};

// --- outgoing attachments -------------------------------------------------

/** The Photo size setting as the attach sheet offers it. */
export type PrepareSize = 'small' | 'medium' | 'large' | 'original';

/** The longest edge kept for each downscaled size, in image pixels. */
export const PREPARE_EDGE: Record<Exclude<PrepareSize, 'original'>, number> = {
	small: 1024,
	medium: 1600,
	large: 2560
};

export type PreparedImage = { blob: Blob; name: string; mime: string };

/** The browser pieces for outgoing preparation, injected so the logic runs without a canvas. */
export type PrepareDeps = {
	decode: (file: Blob) => Promise<Decoded>;
	encode: (img: Decoded, width: number, height: number, type: 'image/jpeg' | 'image/png') => Promise<Blob | null>;
};

// Formats that can carry transparency stay PNG so a logo keeps its alpha; every
// other source becomes a JPEG. GIF is left alone because a re-encode would lose
// its animation; SVG is refused by the server's type deny list.
const PREPARE_ALPHA = new Set(['image/png', 'image/webp']);
const PREPARE_BYPASS = new Set(['image/gif']);

/** The whole image scaled to fit the chosen longest edge, never upscaled. */
export function targetSize(width: number, height: number, size: PrepareSize): { width: number; height: number } {
	if (size === 'original') return { width, height };
	const edge = PREPARE_EDGE[size];
	const longest = Math.max(width, height);
	if (longest <= edge) return { width, height };
	const scale = edge / longest;
	return { width: Math.max(1, Math.round(width * scale)), height: Math.max(1, Math.round(height * scale)) };
}

/**
 * Prepare an outgoing photo: decode, apply EXIF rotation, downscale to the
 * chosen size and re-encode, which strips EXIF and location by construction.
 * A non-image, an animated GIF, or Original size with location removal off is
 * returned byte-for-byte.
 */
export async function prepareImage(file: Blob, opts: { size: PrepareSize; stripLocation: boolean }, deps: PrepareDeps = browserPrepareDeps): Promise<PreparedImage> {
	const name = file instanceof File ? file.name : 'attachment';
	const type = (file.type || '').toLowerCase();
	if (!type.startsWith('image/') || PREPARE_BYPASS.has(type)) {
		return { blob: file, name, mime: type || 'application/octet-stream' };
	}
	if (opts.size === 'original' && !opts.stripLocation) {
		return { blob: file, name, mime: type };
	}
	let img: Decoded;
	try {
		img = await deps.decode(file);
	} catch {
		throw new PhotoError("This browser can't read that photo. Try a JPEG or PNG.");
	}
	try {
		if (!(img.width > 0 && img.height > 0)) throw new PhotoError('That image has nothing in it.');
		const { width, height } = targetSize(img.width, img.height, opts.size);
		const outType = PREPARE_ALPHA.has(type) ? 'image/png' : 'image/jpeg';
		const out = await deps.encode(img, width, height, outType);
		if (!out) throw new PhotoError("That photo couldn't be resized.");
		return { blob: out, name: renameFor(name, outType), mime: outType };
	} finally {
		img.close?.();
	}
}

function renameFor(name: string, type: 'image/jpeg' | 'image/png'): string {
	const ext = type === 'image/png' ? '.png' : '.jpg';
	const dot = name.lastIndexOf('.');
	return (dot > 0 ? name.slice(0, dot) : name) + ext;
}

const browserPrepareDeps: PrepareDeps = {
	decode: (file) => createImageBitmap(file, { imageOrientation: 'from-image' }),
	encode(img, width, height, type) {
		const canvas = document.createElement('canvas');
		canvas.width = width;
		canvas.height = height;
		const ctx = canvas.getContext('2d');
		if (!ctx) return Promise.resolve(null);
		ctx.imageSmoothingQuality = 'high';
		ctx.drawImage(img as ImageBitmap, 0, 0, width, height);
		return new Promise((resolve) => canvas.toBlob(resolve, type, 0.85));
	}
};
