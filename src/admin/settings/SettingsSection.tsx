import type { ReactNode } from "react";

type SettingsSectionProps = {
  id: string;
  index: string;
  icon: ReactNode;
  title: string;
  description: string;
  children: ReactNode;
};

export function SettingsSection({
  id,
  index,
  icon,
  title,
  description,
  children,
}: SettingsSectionProps) {
  return (
    <section
      id={id}
      className="admin-config-section"
      role="tabpanel"
      aria-labelledby={`${id}-tab`}
    >
      <header className="admin-config-section__header">
        <div className="admin-config-section__title-row">
          <span className="admin-config-section__index">{index}</span>
          <span className="admin-config-section__icon" aria-hidden="true">
            {icon}
          </span>
        </div>
        <div className="admin-config-section__heading">
          <h2>{title}</h2>
          <p>{description}</p>
        </div>
      </header>
      <div className="admin-config-section__body">{children}</div>
    </section>
  );
}

type SettingsRowProps = {
  label: string;
  description?: ReactNode;
  labelID?: string;
  descriptionID?: string;
  htmlFor?: string;
  layout?: "responsive" | "inline";
  children: ReactNode;
};

export function SettingsRow({
  label,
  description,
  labelID,
  descriptionID,
  htmlFor,
  layout = "responsive",
  children,
}: SettingsRowProps) {
  return (
    <div
      className={`admin-config-row${layout === "inline" ? " admin-config-row--inline" : ""}`}
    >
      <div className="admin-config-row__copy">
        {htmlFor ? (
          <label id={labelID} htmlFor={htmlFor}>
            {label}
          </label>
        ) : (
          <span id={labelID}>{label}</span>
        )}
        {description != null && <p id={descriptionID}>{description}</p>}
      </div>
      {children}
    </div>
  );
}
