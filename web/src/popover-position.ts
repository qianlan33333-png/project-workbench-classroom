type Placement = {
  side?: "top" | "bottom" | "right";
  align?: "start" | "end";
  gap?: number;
  width?: number;
  maxWidth?: number;
  maxHeight?: number;
};

// Top-layer panels use viewport coordinates; never inherit a clipping parent.
export function positionPopover(
  panel: HTMLElement,
  anchor: HTMLElement,
  options: Placement = {},
) {
  const view = window.visualViewport;
  const left = (view?.offsetLeft ?? 0) + 8,
    top = (view?.offsetTop ?? 0) + 8;
  const right = left + (view?.width ?? window.innerWidth) - 16;
  const bottom = top + (view?.height ?? window.innerHeight) - 16;
  const gap = options.gap ?? 4,
    rect = anchor.getBoundingClientRect();
  panel.style.maxWidth = `${Math.min(right - left, options.maxWidth ?? Infinity)}px`;
  panel.style.maxHeight = `${Math.min(bottom - top, options.maxHeight ?? Infinity)}px`;
  if (options.width !== undefined)
    panel.style.width = `${Math.min(right - left, options.width)}px`;
  let size = panel.getBoundingClientRect();
  if (options.side === "right") {
    const availableRight = Math.max(0, right - rect.right - gap),
      availableLeft = Math.max(0, rect.left - left - gap);
    const flip = availableRight < size.width && availableLeft > availableRight;
    panel.style.maxWidth = `${Math.min(options.maxWidth ?? Infinity, flip ? availableLeft : availableRight)}px`;
    size = panel.getBoundingClientRect();
    panel.style.left = `${Math.max(left, Math.min(flip ? rect.left - size.width - gap : rect.right + gap, right - size.width))}px`;
    panel.style.top = `${Math.max(top, Math.min(rect.top, bottom - size.height))}px`;
  } else {
    const below = Math.max(0, bottom - rect.bottom - gap),
      above = Math.max(0, rect.top - top - gap);
    const upwards =
      options.side === "top"
        ? above >= size.height || (below < size.height && above >= below)
        : below < size.height && above > below;
    const height = Math.min(size.height, upwards ? above : below);
    panel.style.maxHeight = `${height}px`;
    panel.style.left = `${Math.max(left, Math.min(options.align === "start" ? rect.left : rect.right - size.width, right - size.width))}px`;
    panel.style.top = `${Math.max(top, Math.min(upwards ? rect.top - height - gap : rect.bottom + gap, bottom - height))}px`;
  }
  return rect;
}
