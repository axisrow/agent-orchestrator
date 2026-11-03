export type PRDisplayTone = "neutral" | "passive" | "success" | "validating" | "review" | "warning" | "error";

export type PRSummaryPartKey = "ci" | "review" | "merge";
export type PRNoun = "check" | "comment" | "file" | "line" | "reason" | "reviewer";

export type PRStatusRow = {
	key: PRSummaryPartKey;
	label: string;
	value: string;
	detail?: string;
	tone: PRDisplayTone;
};

export type PRSummaryLink = {
	label: string;
	href?: string;
	title?: string;
};

export type PRSummaryPart = {
	key: PRSummaryPartKey;
	label: string;
	status: string;
	summary?: string;
	links: PRSummaryLink[];
	linkTotal?: number;
	overflowLabel?: string;
	overflowNoun?: PRNoun;
	tone: PRDisplayTone;
};

export type PRCardStatus = {
	key: "ci" | "merge" | "review" | "lifecycle" | "ao_review";
	label: string;
	detail?: string;
	href?: string;
	breathe?: boolean;
	/** ISO timestamp the in-flight status began (the CI fact last changed); breathe rows render a live elapsed label from it. */
	since?: string;
	timeUnavailable?: string;
	links: PRSummaryLink[];
	tone: PRDisplayTone;
};

export type PRCardPresentation = {
	primary: PRCardStatus;
	supporting: PRCardStatus[];
	/** Compact, priority-ordered rows used by pull-request cards. */
	statusRows?: PRCardStatus[];
	readiness?: {
		label: string;
		/** Only when the verdict says something the status rows above do not. */
		detail?: string;
		tone: PRDisplayTone;
	};
};

/** The person behind a pull request AO opened through its GitHub App. */
export type PROpener = {
	name: string;
	githubLogin?: string;
	avatarUrl?: string;
};

export type PRSummaryMetadata = {
	provider: string;
	author?: string;
	authorAvatarUrl?: string;
	/** Set when AO's GitHub App opened the PR, so `author` is its bot account. */
	openedBy?: PROpener;
	sourceBranch?: string;
	targetBranch?: string;
	changedFiles: number;
	additions: number;
	deletions: number;
};
