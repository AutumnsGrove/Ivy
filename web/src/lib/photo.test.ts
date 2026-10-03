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
