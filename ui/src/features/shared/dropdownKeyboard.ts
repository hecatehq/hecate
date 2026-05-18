export function focusInitialDropdownItem(menu: HTMLElement | null) {
  const selectedItem = menu?.querySelector<HTMLButtonElement>('[data-selected="true"]');
  const firstItem = menu?.querySelector<HTMLButtonElement>("[data-dropdown-item]");
  (selectedItem ?? firstItem)?.focus();
}

export function focusDropdownItem(menu: HTMLElement | null, key: string) {
  if (!menu) return;
  const items = Array.from(menu.querySelectorAll<HTMLButtonElement>("[data-dropdown-item]"));
  if (items.length === 0) return;
  const currentIndex = Math.max(
    0,
    items.findIndex((item) => item === document.activeElement),
  );
  const nextIndex = (() => {
    switch (key) {
      case "ArrowDown":
        return (currentIndex + 1) % items.length;
      case "ArrowUp":
        return (currentIndex - 1 + items.length) % items.length;
      case "Home":
        return 0;
      case "End":
        return items.length - 1;
      default:
        return currentIndex;
    }
  })();
  items[nextIndex]?.focus();
}
