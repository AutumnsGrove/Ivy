import type { PageLoad } from './$types';

// The message itself loads inside MessageLoader so a failed body can sit under a real header.
export const load: PageLoad = ({ params }) => ({ id: params.id });
