import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { WorkspaceInitials } from './workspace-initials';
import { workspaceInitials } from '@/lib/workspace-initials';

describe('workspace initials', () => {
  it.each([
    ['Flow', 'FL'], ['Flow Team', 'FT'], ['中文团队', '中文'],
    ['中a', '中A'], ['a中', 'A中'], ['张三 李四', '张李'],
    ['équipe', 'ÉQ'], ['', 'W'],
  ])('keeps whole initials for %s', (name, expected) => {
    expect(workspaceInitials(name)).toBe(expected);
  });

  it.each(['中文团队', '中a', 'a中'])('fits wide pairs in the shared avatar for %s', name => {
    render(<WorkspaceInitials name={name} />);
    expect(screen.getByText(workspaceInitials(name))).toHaveAttribute('data-wide', 'true');
  });

  it('keeps a single symbol at the normal avatar size', () => {
    render(<WorkspaceInitials name="Flow" icon="中" />);
    expect(screen.getByText('中')).not.toHaveAttribute('data-wide');
  });
});
