import type { Location } from "react-router";

const RETAINED_LISTING_STATE_KEY = "videoListingBackground";

// History state can survive a browser reload. The document id makes retained
// React trees explicitly same-document only; a direct/reloaded detail URL must
// render as an ordinary standalone page.
const LISTING_BACKGROUND_DOCUMENT_ID =
  typeof globalThis.crypto?.randomUUID === "function"
    ? globalThis.crypto.randomUUID()
    : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;

type RetainedListingState = {
  documentID: string;
  location: Location;
};

export type VideoDetailNavigationState = {
  from: string;
  [RETAINED_LISTING_STATE_KEY]?: RetainedListingState;
};

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

export function isVideoListingPath(pathname: string): boolean {
  const normalized =
    pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  return normalized === "/" || normalized.toLowerCase() === "/list";
}

function retainedListingState(state: unknown): RetainedListingState | null {
  if (!isObject(state)) return null;
  const retained = state[RETAINED_LISTING_STATE_KEY];
  if (!isObject(retained)) return null;
  if (retained.documentID !== LISTING_BACKGROUND_DOCUMENT_ID) return null;

  const location = retained.location;
  if (!isObject(location)) return null;
  if (
    typeof location.pathname !== "string" ||
    !isVideoListingPath(location.pathname) ||
    typeof location.search !== "string" ||
    typeof location.hash !== "string" ||
    typeof location.key !== "string"
  ) {
    return null;
  }

  return {
    documentID: LISTING_BACKGROUND_DOCUMENT_ID,
    location: {
      pathname: location.pathname,
      search: location.search,
      hash: location.hash,
      state: location.state ?? null,
      key: location.key,
    },
  };
}

/** Build state for a card opened from HomePage or ListingPage. */
export function createVideoDetailNavigationState(
  from: string,
  location: Location
): VideoDetailNavigationState {
  if (!isVideoListingPath(location.pathname)) return { from };
  return {
    from,
    [RETAINED_LISTING_STATE_KEY]: {
      documentID: LISTING_BACKGROUND_DOCUMENT_ID,
      location: {
        pathname: location.pathname,
        search: location.search,
        hash: location.hash,
        state: location.state,
        key: location.key,
      },
    },
  };
}

/** Keep the original listing alive while navigating between detail videos. */
export function continueVideoDetailNavigationState(
  from: string,
  currentState: unknown
): VideoDetailNavigationState {
  const retained = retainedListingState(currentState);
  return retained
    ? {
        from,
        [RETAINED_LISTING_STATE_KEY]: retained,
      }
    : { from };
}

export function readVideoListingBackground(
  state: unknown
): Location | null {
  return retainedListingState(state)?.location ?? null;
}
