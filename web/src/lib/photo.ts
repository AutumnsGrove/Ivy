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
