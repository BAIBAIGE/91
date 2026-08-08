import { Link } from "react-router";
import { subNavLinks } from "@/data/tags";

export function SubNav() {
  if (subNavLinks.length === 0) return null;
  return (
    <div className="sub-nav">
      <div className="container">
        <ul className="sub-nav__list">
          {subNavLinks.map((link) => (
            <li key={link.href}>
              <Link to={link.href} className="sub-nav__item">
                {link.label}
              </Link>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
