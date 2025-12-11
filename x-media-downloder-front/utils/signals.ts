// x-media-downloder-front/utils/signals.ts

import { signal } from "@preact/signals";
import type { Image } from "./types.ts";

/**
 * Signal for the currently selected image to be displayed in the modal.
 */
export const selectedImage = signal<Image | null>(null);

/**
 * Signal for the index of the currently selected image within the gallery.
 */
export const selectedImageIndex = signal<number>(-1);

/**
 * Signal for the complete list of images currently displayed in the gallery.
 * This is used by the modal for next/previous navigation.
 */
export const allGalleryImages = signal<Image[]>([]);
