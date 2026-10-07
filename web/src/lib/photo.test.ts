import { describe, expect, it, vi } from 'vitest';
import { PhotoError, PHOTO_EDGE, squareCrop, squarePhoto, type PhotoDeps } from './photo';

describe('squareCrop', () => {
	it('takes the centre of a landscape photo', () => {
		expect(squareCrop(4000, 3000)).toEqual({ sx: 500, sy: 0, side: 3000 });
	});

	it('takes the centre of a portrait photo', () => {
		expect(squareCrop(3000, 4000)).toEqual({ sx: 0, sy: 500, side: 3000 });
	});

	it('leaves a square alone', () => {
		expect(squareCrop(800, 800)).toEqual({ sx: 0, sy: 0, side: 800 });
	});

	it('floors odd leftovers so the crop stays inside the image', () => {
		expect(squareCrop(1001, 500)).toEqual({ sx: 250, sy: 0, side: 500 });
	});
});

const blob = (type: string) => new Blob(['x'], { type });

function deps(width: number, height: number, out: Blob | null = blob('image/jpeg')): PhotoDeps & { close: ReturnType<typeof vi.fn> } {
	const close = vi.fn();
	return {
		close,
		decode: vi.fn(async () => ({ width, height, close })),
		encode: vi.fn(async () => out)
	};
}

describe('squarePhoto', () => {
	it('crops to a square no larger than the edge limit', async () => {
		const d = deps(4000, 3000);
		await squarePhoto(blob('image/jpeg'), d);
		expect(d.encode).toHaveBeenCalledWith(expect.anything(), { sx: 500, sy: 0, side: 3000 }, PHOTO_EDGE, 'image/jpeg');
	});

	it('never upscales a small photo', async () => {
		const d = deps(200, 300);
		await squarePhoto(blob('image/jpeg'), d);
		expect(d.encode).toHaveBeenCalledWith(expect.anything(), { sx: 0, sy: 50, side: 200 }, 200, 'image/jpeg');
	});

	it('keeps transparency by encoding PNG, GIF and WebP sources as PNG', async () => {
		for (const type of ['image/png', 'image/gif', 'image/webp']) {
			const d = deps(600, 600, blob('image/png'));
			await squarePhoto(blob(type), d);
			expect(d.encode).toHaveBeenCalledWith(expect.anything(), expect.anything(), expect.anything(), 'image/png');
		}
	});

	it('releases the decoded image', async () => {
		const d = deps(600, 600);
		await squarePhoto(blob('image/jpeg'), d);
		expect(d.close).toHaveBeenCalledOnce();
	});

	it('says so when the browser cannot decode the file (HEIC off Safari, a corrupt image)', async () => {
		const d = deps(1, 1);
		d.decode = vi.fn(async () => {
			throw new Error('bad image');
		});
		await expect(squarePhoto(blob('image/heic'), d)).rejects.toBeInstanceOf(PhotoError);
	});

	it('refuses an image with no area instead of dividing by it', async () => {
		await expect(squarePhoto(blob('image/png'), deps(0, 500))).rejects.toBeInstanceOf(PhotoError);
	});

	it('fails cleanly, and still releases the image, when encoding yields nothing', async () => {
		const d = deps(600, 600, null);
		await expect(squarePhoto(blob('image/jpeg'), d)).rejects.toBeInstanceOf(PhotoError);
		expect(d.close).toHaveBeenCalledOnce();
	});
});

import { prepareImage, PREPARE_EDGE, type PrepareDeps } from './photo';

const file = (name: string, type: string) => new File(['x'], name, { type });

function prepDeps(width: number, height: number, out: Blob | null = blob('image/jpeg')): PrepareDeps & { close: ReturnType<typeof vi.fn> } {
	const close = vi.fn();
	return {
		close,
		decode: vi.fn(async () => ({ width, height, close })),
		encode: vi.fn(async () => out)
	};
}

describe('prepareImage', () => {
	it('downscales to the chosen size, keeping the aspect ratio', async () => {
		const d = prepDeps(4000, 3000);
		const out = await prepareImage(file('IMG_1.HEIC', 'image/heic'), { size: 'medium', stripLocation: true }, d);
		expect(d.encode).toHaveBeenCalledWith(expect.anything(), PREPARE_EDGE.medium, 1200, 'image/jpeg');
		expect(out.name).toBe('IMG_1.jpg');
		expect(out.mime).toBe('image/jpeg');
	});

	it('re-encodes a photo already under the edge, to strip its metadata', async () => {
		const d = prepDeps(800, 600);
		await prepareImage(file('a.jpg', 'image/jpeg'), { size: 'large', stripLocation: true }, d);
		expect(d.encode).toHaveBeenCalledWith(expect.anything(), 800, 600, 'image/jpeg');
	});

	it('keeps the original bytes at Original size when location removal is off', async () => {
		const original = file('a.jpg', 'image/jpeg');
		const d = prepDeps(800, 600);
		const out = await prepareImage(original, { size: 'original', stripLocation: false }, d);
		expect(d.decode).not.toHaveBeenCalled();
		expect(out.blob).toBe(original);
		expect(out.name).toBe('a.jpg');
	});

	it('re-encodes at native size at Original size when location removal is on', async () => {
		const d = prepDeps(800, 600);
		await prepareImage(file('a.heic', 'image/heic'), { size: 'original', stripLocation: true }, d);
		expect(d.encode).toHaveBeenCalledWith(expect.anything(), 800, 600, 'image/jpeg');
	});

	it('keeps transparency by encoding a PNG source as PNG', async () => {
		const d = prepDeps(2000, 2000, blob('image/png'));
		const out = await prepareImage(file('logo.png', 'image/png'), { size: 'medium', stripLocation: true }, d);
		expect(d.encode).toHaveBeenCalledWith(expect.anything(), PREPARE_EDGE.medium, PREPARE_EDGE.medium, 'image/png');
		expect(out.name).toBe('logo.png');
	});

	it('leaves a non-image and an animated GIF untouched', async () => {
		for (const [name, type] of [
			['doc.pdf', 'application/pdf'],
			['anim.gif', 'image/gif']
		]) {
			const original = file(name, type);
			const d = prepDeps(100, 100);
			const out = await prepareImage(original, { size: 'medium', stripLocation: true }, d);
			expect(out.blob).toBe(original);
			expect(d.decode).not.toHaveBeenCalled();
		}
	});

	it('releases the decoded image even when encoding fails', async () => {
		const d = prepDeps(600, 600, null);
		await expect(prepareImage(file('a.jpg', 'image/jpeg'), { size: 'medium', stripLocation: true }, d)).rejects.toBeInstanceOf(PhotoError);
		expect(d.close).toHaveBeenCalledOnce();
	});
});
