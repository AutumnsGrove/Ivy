// The icons an account badge can wear. The stored value is the Lucide name, not
// a glyph, and the server accepts only these names (store/accounticons.go; a
// test fails if the two lists drift). Each is a separate import in icons.ts, so
// the picker costs only the dozen it offers.
import type { Component } from 'svelte';
import { Bird, Cloud, Droplets, Flower, Flower2, Leaf, Mail, Moon, Sprout, Star, Sun, TreeDeciduous } from './icons.js';

export const ACCOUNT_ICONS: { name: string; label: string; icon: Component }[] = [
	{ name: 'leaf', label: 'Leaf', icon: Leaf },
	{ name: 'moon', label: 'Moon', icon: Moon },
	{ name: 'sun', label: 'Sun', icon: Sun },
	{ name: 'flower', label: 'Blossom', icon: Flower },
	{ name: 'flower-2', label: 'Flower', icon: Flower2 },
	{ name: 'sprout', label: 'Sprout', icon: Sprout },
	{ name: 'tree-deciduous', label: 'Tree', icon: TreeDeciduous },
	{ name: 'bird', label: 'Bird', icon: Bird },
	{ name: 'mail', label: 'Mailbox', icon: Mail },
	{ name: 'droplets', label: 'Droplets', icon: Droplets },
	{ name: 'cloud', label: 'Cloud', icon: Cloud },
	{ name: 'star', label: 'Star', icon: Star }
];

// A Map, not an object, so a stored value like "constructor" finds nothing.
const byName = new Map(ACCOUNT_ICONS.map((i) => [i.name, i.icon]));

/**
 * The component for a stored icon name, or undefined for none, a name this build
 * does not know, or an old glyph, so the badge falls back to the initial instead
 * of breaking.
 */
export function accountIcon(name: string | undefined): Component | undefined {
	return name ? byName.get(name) : undefined;
}
