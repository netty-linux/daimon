import { copy } from '../../i18n/copy';
import { useState } from 'react';
import type { Bot, ProviderSummary } from '../../api/types';
import { ComputerPanel } from '../../components/ComputerPanel';
import { SandboxPanel } from '../../components/SandboxPanel';
import { MCPPanel } from '../../components/MCPPanel';
import { ProductTabs } from '../../components/ProductTabs';
import { pt } from '../../i18n/pt-BR';

const sections = [
  { id: 'general', label: copy["Geral"] }, { id: 'bots', label: copy["Bots"] },
  { id: 'models', label: copy["Modelos / Provedores"] }, { id: 'mcp', label: copy["MCP"] },
  { id: 'computer', label: copy["Computador"] }, { id: 'sandboxes', label: copy["Computadores isolados"] },
  { id: 'cloud', label: copy["Nuvem"] }, { id: 'memory', label: copy["Memória"] }, { id: 'system', label: copy["Sistema"] },
] as const;
export type SettingsSection = typeof sections[number]['id'];
export function Settings({ initial, providers, bot, onEdit, onClose, onRefresh }: {
  initial: SettingsSection; providers: ProviderSummary[]; bot?: Bot;
  onEdit: () => void; onClose: () => void; onRefresh: () => void;
}) {
  const [section, setSection] = useState<SettingsSection>(initial);
  return <section className="settings-surface"><header><h2>{pt.settings}</h2><button onClick={onClose}>{pt.close}</button></header>
    <ProductTabs id="settings" label={pt.settings} items={sections} value={section} onChange={setSection} />
    <div role="tabpanel" id={`settings-panel-${section}`} aria-labelledby={`settings-tab-${section}`} tabIndex={0}>
      {section === 'general' && <><h3>{copy["Seu DAIMON"]}</h3><p>{copy["Um espaço local para seus bots, conversas e tarefas."]}</p><p className="hint">{copy["Tema escuro · Português do Brasil"]}</p></>}
      {section === 'bots' && <><h3>{bot?.name ?? pt.bots}</h3><p>{copy["Instruções, modelo e recursos de cada assistente."]}</p><button disabled={!bot} onClick={onEdit}>{pt.editBot}</button></>}
      {section === 'models' && <><h3>{copy["Modelos disponíveis no servidor"]}</h3><p>{copy["Credenciais e conexão são configuradas fora do navegador."]}</p>{!providers.length && <p role="status">{pt.noModel}</p>}
        <details><summary>{copy["Provedores configurados"]}</summary>{providers.map(provider => <p key={provider.id}>{provider.id === 'openai' ? 'OpenAI-compatible' : provider.id === 'groq' ? 'Groq' : provider.id}</p>)}<p className="hint">{copy["A lista identifica conexões; os nomes dos modelos são definidos ao criar o bot. Não há catálogo automático."]}</p></details></>}
      {section === 'mcp' && <MCPPanel />}
      {section === 'computer' && <ComputerPanel />}
      {section === 'sandboxes' && <SandboxPanel />}
      {section === 'cloud' && <><h3>{copy["Computador na nuvem"]}</h3><p>{pt.cloudCost}</p><SandboxPanel /></>}
      {section === 'memory' && <><h3>{copy["Memórias sob seu controle"]}</h3><p>{copy["Salve, edite e exclua memórias pela aba Memória. Nenhuma memória é criada automaticamente."]}</p><p className="hint">{copy["Os registros são locais e podem ser enviados ao modelo nas próximas tarefas."]}</p></>}
      {section === 'system' && <><h3>{copy["Execução local"]}</h3><p>{copy["O navegador se conecta ao DAIMON neste computador."]}</p><details><summary>{pt.technical}</summary><p className="hint">{copy["HTTP local · SSE · configurações explícitas do servidor. Não há conta, sincronização de perfil ou credenciais no navegador."]}</p></details><button onClick={onRefresh}>{copy["Atualizar recursos"]}</button></>}
    </div>
  </section>;
}
