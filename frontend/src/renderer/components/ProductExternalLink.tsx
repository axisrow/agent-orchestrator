import { openLinkInSystemBrowser } from "../lib/external-link-policy";
import { AppLink } from "./AppLink";
import type { ExternalLinkProps } from "@aoagents/product-ui";

export function ProductExternalLink({
	ariaLabel,
	children,
	stopPropagation,
	forceSystemBrowser,
	...props
}: ExternalLinkProps) {
	return (
		<AppLink
			{...props}
			aria-label={ariaLabel}
			onClick={(event) => {
				if (stopPropagation) event.stopPropagation();
				if (forceSystemBrowser) { event.preventDefault(); void openLinkInSystemBrowser(props.href); }
			}}
			onPointerDown={stopPropagation ? (event) => event.stopPropagation() : undefined}
			rel="noopener noreferrer"
			target="_blank"
		>
			{children}
		</AppLink>
	);
}
