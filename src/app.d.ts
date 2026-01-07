// See https://kit.svelte.dev/docs/types#app
// for information about these interfaces
declare global {
	namespace App {
		// interface Error {}
		interface Locals {
			user?: {
				id: string;
				email: string;
				name?: string;
				image?: string;
			};
			session?: {
				id: string;
				userId: string;
				expiresAt: number;
			};
		}
		// interface PageData {}
		// interface PageState {}
		interface Platform {
			env: {
				DB: D1Database;
				R2: R2Bucket;
				KV: KVNamespace;
			};
		}
	}
}

export {};
