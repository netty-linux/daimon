import { useState, type ComponentProps } from 'react';
import { BotNavigation, ConversationNavigation } from './Navigation';
import { penCopy } from '../../i18n/pen';

export function WorkspaceNavigation({ bots, conversations, hideConversations, hideBots }: {
  bots: ComponentProps<typeof BotNavigation>;
  conversations: ComponentProps<typeof ConversationNavigation>;
  hideConversations: boolean;
  hideBots: boolean;
}) {
  const [query, setQuery] = useState('');
  const match = (text: string) => text.toLocaleLowerCase('pt-BR').includes(query.trim().toLocaleLowerCase('pt-BR'));
  return <aside className="workspace-navigation" aria-label={penCopy.navigation}>
    <label className="navigation-search"><span className="sr-only">{penCopy.search}</span><input type="search" value={query} placeholder={penCopy.searchPlaceholder} onChange={event => setQuery(event.target.value)} /></label>
    {!hideBots && <BotNavigation {...bots} bots={bots.bots.filter(bot => match(bot.name))} />}
    {!hideConversations && <ConversationNavigation {...conversations} threads={conversations.threads.filter(thread => match(thread.title))} />}
  </aside>;
}
