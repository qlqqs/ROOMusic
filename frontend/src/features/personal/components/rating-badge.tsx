import { Star } from "lucide-react";
import type { FavoriteKind } from "../api/decoders";
import { useRatingStatus } from "./rating-status-scope";
import styles from "./rating-badge.module.css";

/** 曲目行里只读的小星：评过分才显示，读作“N 星”；没评过、还没读到时什么也不显示。 */
export function RatingBadge({ kind, id }: { kind: FavoriteKind; id: string }) {
  const rating = useRatingStatus().ratingOf(kind, id);
  if (rating === undefined || rating === 0) return null;
  return (
    <span className={styles.badge} role="img" aria-label={`${rating} 星`} data-rating-badge="">
      {Array.from({ length: rating }, (_, index) => (
        <Star key={index} size={10} strokeWidth={1.5} aria-hidden="true" />
      ))}
    </span>
  );
}
