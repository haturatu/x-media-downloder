interface PaginationProps {
  currentPage: number;
  totalPages: number;
  onPageChange: (page: number) => void;
}

export default function Pagination(
  { currentPage, totalPages, onPageChange }: PaginationProps,
) {
  if (totalPages <= 1) return null;

  const pages: (number | "...")[] = [];
  const windowSize = 2;
  const start = Math.max(2, currentPage - windowSize);
  const end = Math.min(totalPages - 1, currentPage + windowSize);

  pages.push(1);
  if (start > 2) pages.push("...");
  for (let i = start; i <= end; i++) pages.push(i);
  if (end < totalPages - 1) pages.push("...");
  if (totalPages > 1) pages.push(totalPages);

  return (
    <div class="pagination">
      <button
        type="button"
        disabled={currentPage === 1}
        onClick={() => onPageChange(currentPage - 1)}
        class="btn btn-secondary"
      >
        Previous
      </button>
      {pages.map((page, index) => (
        page === "..."
          ? <span key={`${page}-${index}`} class="page-gap">...</span>
          : (
            <button
              type="button"
              key={page}
              onClick={() => onPageChange(page)}
              class={`page-btn ${currentPage === page ? "active" : ""}`}
            >
              {page}
            </button>
          )
      ))}
      <button
        type="button"
        disabled={currentPage === totalPages}
        onClick={() => onPageChange(currentPage + 1)}
        class="btn btn-secondary"
      >
        Next
      </button>
    </div>
  );
}
