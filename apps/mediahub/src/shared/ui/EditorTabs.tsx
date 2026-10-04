export interface EditorTab {
  id: string;
  label: string;
}

export function EditorTabs({
  tabs,
  value,
  onChange,
}: {
  tabs: readonly EditorTab[];
  value: string;
  onChange: (id: string) => void;
}) {
  return (
    <div role="tablist" className="sticky top-0 z-10 -mx-4 flex gap-2 overflow-x-auto bg-bg px-4 py-2">
      {tabs.map((tab) => {
        const selected = tab.id === value;
        return (
          <button
            key={tab.id}
            type="button"
            role="tab"
            id={`tab-${tab.id}`}
            aria-selected={selected}
            aria-controls={`panel-${tab.id}`}
            className={`min-h-11 shrink-0 rounded-full px-4 text-sm font-semibold ${
              selected ? 'bg-accent text-accent-fg' : 'bg-surface-2 text-fg'
            }`}
            onClick={() => onChange(tab.id)}
          >
            {tab.label}
          </button>
        );
      })}
    </div>
  );
}
