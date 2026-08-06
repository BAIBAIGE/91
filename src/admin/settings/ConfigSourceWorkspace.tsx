import {
  lazy,
  Suspense,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import type { ReactCodeMirrorRef } from "@uiw/react-codemirror";
import { ChevronDown, ChevronUp, Search } from "lucide-react";

type Props = {
  value: string;
  error: string;
  disabled: boolean;
  onChange: (value: string) => void;
};

type SearchDirection = "next" | "previous";

const loadConfigSourceEditor = () => import("./ConfigSourceEditor");
const LazyConfigSourceEditor = lazy(loadConfigSourceEditor);

export function preloadConfigSourceEditor() {
  return loadConfigSourceEditor();
}

function findMatches(source: string, query: string) {
  const matches: number[] = [];
  const normalizedSource = source.toLocaleLowerCase();
  const normalizedQuery = query.toLocaleLowerCase();
  let position = 0;

  while (position < normalizedSource.length) {
    const index = normalizedSource.indexOf(normalizedQuery, position);
    if (index === -1) break;
    matches.push(index);
    position = index + Math.max(normalizedQuery.length, 1);
  }
  return matches;
}

export function ConfigSourceWorkspace({ value, error, disabled, onChange }: Props) {
  const editorRef = useRef<ReactCodeMirrorRef | null>(null);
  const [query, setQuery] = useState("");
  const [lastQuery, setLastQuery] = useState("");
  const [searchResult, setSearchResult] = useState({ current: 0, total: 0 });

  function resetSearch() {
    setLastQuery("");
    setSearchResult({ current: 0, total: 0 });
  }

  function performSearch(direction: SearchDirection) {
    const view = editorRef.current?.view;
    if (!view || !query) return;

    const matches = findMatches(view.state.doc.toString(), query);
    setLastQuery(query);
    if (matches.length === 0) {
      setSearchResult({ current: 0, total: 0 });
      return;
    }

    const selection = view.state.selection.main;
    let matchIndex = -1;
    if (direction === "next") {
      matchIndex = matches.findIndex((position) => position >= selection.to);
      if (matchIndex === -1) matchIndex = 0;
    } else {
      for (let index = matches.length - 1; index >= 0; index -= 1) {
        if (matches[index] < selection.from) {
          matchIndex = index;
          break;
        }
      }
      if (matchIndex === -1) matchIndex = matches.length - 1;
    }

    const position = matches[matchIndex];
    view.dispatch({
      selection: { anchor: position, head: position + query.length },
      scrollIntoView: true,
    });
    view.focus();
    setSearchResult({ current: matchIndex + 1, total: matches.length });
  }

  function handleSearchKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key !== "Enter") return;
    event.preventDefault();
    performSearch(event.shiftKey ? "previous" : "next");
  }

  function handleEditorChange(nextValue: string) {
    onChange(nextValue);
    if (lastQuery) resetSearch();
  }

  const searchedCurrentQuery = query.length > 0 && lastQuery === query;
  const canNavigate = searchedCurrentQuery && searchResult.total > 0 && !disabled;

  return (
    <div className="admin-config-source">
      <div className="admin-config-source__toolbar">
        <div className="admin-config-source__search">
          <label htmlFor="config-source-search" className="sr-only">
            搜索配置内容
          </label>
          <input
            id="config-source-search"
            type="search"
            value={query}
            placeholder="搜索配置内容..."
            disabled={disabled}
            onChange={(event) => {
              setQuery(event.target.value);
              resetSearch();
            }}
            onKeyDown={handleSearchKeyDown}
          />
          <div className="admin-config-source__search-right">
            {searchedCurrentQuery && (
              <span className="admin-config-source__search-count">
                {searchResult.total > 0
                  ? `${searchResult.current} / ${searchResult.total}`
                  : "无结果"}
              </span>
            )}
            <button
              type="button"
              className="admin-config-source__search-submit"
              title="搜索"
              aria-label="搜索配置内容"
              disabled={!query || disabled}
              onClick={() => performSearch("next")}
            >
              <Search size={16} aria-hidden="true" />
            </button>
          </div>
        </div>

        <div className="admin-config-source__search-actions">
          <button
            type="button"
            title="上一个"
            aria-label="上一个匹配项"
            disabled={!canNavigate}
            onClick={() => performSearch("previous")}
          >
            <ChevronUp size={16} aria-hidden="true" />
          </button>
          <button
            type="button"
            title="下一个"
            aria-label="下一个匹配项"
            disabled={!canNavigate}
            onClick={() => performSearch("next")}
          >
            <ChevronDown size={16} aria-hidden="true" />
          </button>
        </div>
      </div>

      <div className={`admin-config-source__editor${error ? " has-error" : ""}`}>
        <Suspense fallback={null}>
          <LazyConfigSourceEditor
            editorRef={editorRef}
            value={value}
            disabled={disabled}
            onChange={handleEditorChange}
          />
        </Suspense>
      </div>

      {error && <p className="admin-config-source__hint is-error">{error}</p>}
    </div>
  );
}
