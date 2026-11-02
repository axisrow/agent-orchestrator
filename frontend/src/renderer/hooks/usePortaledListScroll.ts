import { useCallback } from "react";

/**
 * A portaled menu (or popover) list renders beside any modal Dialog, and the
 * Dialog's react-remove-scroll document-level (passive:false) wheel listener
 * preventDefaults every wheel whose target is outside the dialog — so the
 * browser never scrolls the portaled list natively. Drive the scroll here
 * instead: apply the wheel delta to scrollTop directly (immune to
 * preventDefault) and cancel the native scroll so nothing double-scrolls when
 * no lock is active. Same trade as SearchablePicker's portaled list, and for
 * the same reason; a native non-passive listener is required because React
 * registers `wheel` passively, which makes preventDefault() from a React
 * onWheel handler a no-op. Guarded on real overflow so short (non-scrolling)
 * lists behave normally.
 */
export function usePortaledListScroll(): (element: HTMLDivElement | null) => (() => void) | undefined {
	return useCallback((element: HTMLDivElement | null) => {
		if (!element) return;
		const onWheel = (event: WheelEvent) => {
			if (element.scrollHeight <= element.clientHeight) return;
			const delta =
				event.deltaMode === 1 ? event.deltaY * 16 : event.deltaMode === 2 ? event.deltaY * element.clientHeight : event.deltaY;
			element.scrollTop += delta;
			event.preventDefault();
		};
		element.addEventListener("wheel", onWheel, { passive: false });
		return () => element.removeEventListener("wheel", onWheel);
	}, []);
}
