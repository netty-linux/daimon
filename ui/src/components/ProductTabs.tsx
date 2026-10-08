import { useRef } from 'react';

export function ProductTabs<T extends string>({ id, label, items, value, onChange }: {
  id: string; label: string; items: readonly { id: T; label: string }[];
  value: T; onChange: (value: T) => void;
}) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  return <div role="tablist" aria-label={label} className="product-tabs">{items.map((item, index) =>
    <button type="button" key={item.id} ref={node => { refs.current[index] = node; }} role="tab"
      id={`${id}-tab-${item.id}`} aria-controls={`${id}-panel-${item.id}`}
      aria-selected={value === item.id} tabIndex={value === item.id ? 0 : -1}
      onClick={() => onChange(item.id)} onKeyDown={event => {
        let next = index;
        if (event.key === 'ArrowRight') next = (index + 1) % items.length;
        else if (event.key === 'ArrowLeft') next = (index + items.length - 1) % items.length;
        else if (event.key === 'Home') next = 0;
        else if (event.key === 'End') next = items.length - 1;
        else return;
        event.preventDefault(); onChange(items[next].id); refs.current[next]?.focus();
      }}>{item.label}</button>)}</div>;
}
