import { Image } from "../utils/types.ts";

interface ImageGridProps {
  images: Image[];
  onImageClick?: (image: Image, index: number) => void;
}

export default function ImageGrid({ images, onImageClick }: ImageGridProps) {
  if (images.length === 0) {
    return <p class="placeholder">No images found for this query.</p>;
  }

  return (
    <div class="image-grid">
      {images.map((image, index) => (
        <div
          key={image.path}
          class="img-container"
          onClick={() => onImageClick && onImageClick(image, index)}
        >
          <img
            src={`/images/${image.path}`}
            alt="media"
            loading="lazy"
          />
          <div class="tags-overlay">
            {image.tags?.map((tag) => tag.tag).join(", ") || "No Tags"}
          </div>
        </div>
      ))}
    </div>
  );
}
