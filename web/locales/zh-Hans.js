// zh-Hans.js — the Simplified Chinese interface strings.
//
// Simplified Chinese is the one locale here written in CJK, and it is also the
// language the board's own notation comes from. The two are separate on purpose:
// the pieces and the river keep their Chinese characters in every locale,
// because that is board notation, while this table is the interface around them
// and is translated.
//
// Keys, placeholders and markup are the same as en.js; that table is the
// fallback, and testdata/ holds this one to its key set.
globalThis.XQ_LOCALES = Object.assign(globalThis.XQ_LOCALES || {}, {
  'zh-Hans': {
    'app.name': '象棋',
    // The Chinese-language API reference, which is the one a reader of this
    // locale wants; en.js and vi.js point at the English one.
    'app.tagline': '与 <strong>godogpaw</strong> 引擎对弈，并使用 <a href="https://www.chessdb.cn/cloudbook_api.html" target="_blank" rel="noreferrer">chessdb.cn</a> 作为开局库。',

    'board.label': '象棋棋盘',

    'status.loading': '加载中…',
    'status.yourTurn': '轮到{side}走 — 轮到你',
    'status.yourTurnCheck': '轮到{side}走 — 轮到你（你正被将军）',
    'status.computerTurn': '轮到{side}走 — 轮到电脑',
    'status.thinking': '电脑正在思考…',
    'status.draw': '和棋 — {reason}',
    'status.winYou': '{winner}以{reason}获胜 — 你赢了',
    'status.winComputer': '{winner}以{reason}获胜 — 电脑赢了',

    'side.red': '红方',
    'side.black': '黑方',

    // How a game ended, keyed by the reason code the server sends.
    'reason.checkmate': '将死',
    'reason.stalemate': '困毙',
    'reason.perpetual_check': '长将或长捉',
    'reason.repetition': '三次重复局面',
    'reason.sixty_move': '六十回合规则',
    'reason.unknown': '未知原因',

    'aiMove.played': '电脑走了 <strong>{move}</strong>',
    'aiMove.detail': ' — {stats}',
    'badge.book': '开局库',
    'badge.search': '搜索',
    'badge.fallback': '降级',
    'stat.depth': '深度 {n}',
    'stat.score': '分数 {n}',
    'stat.nodes': '{n} 个节点',
    'stat.time': '{n} 毫秒',
    // The locale's own list separator, which is not the Latin comma.
    'list.separator': '，',

    'pov.red': '分数和胜率以红方视角计算。',
    'pov.black': '分数和胜率以黑方视角计算。',

    'analysis.heading': '分析',
    'analysis.cloudBest': '云端最佳',
    'analysis.cloudEval': '云端评估',
    'analysis.cloudDepth': '云端深度',
    'analysis.topMoves': '开局库首选着法',
    'analysis.pv': '主要变例',
    'analysis.plies': '{n} 个半回合',
    'analysis.notInBook': '当前局面不在云端开局库中。',
    'analysis.partial': '部分云端查询失败；此处显示可能不完整。',
    'analysis.unavailable': '云端开局库不可用：{error}',

    'table.move': '着法',
    'table.score': '分数',
    'table.winrate': '胜率 %',
    'table.note': '备注',

    'button.newGame': '新对局',
    'button.undo': '悔棋',
    'button.hint': '提示',

    'settings.language': '语言',
    'settings.playAs': '执子',
    'settings.sideRed': '红方（先手）',
    'settings.sideBlack': '黑方（后手）',
    'settings.level': '难度',
    'settings.useBook': '使用开局库',
    'settings.bookNote': '开局阶段，无论何种难度，开局库都提供电脑可选的着法，因此最简单的难度也会走出棋手熟悉的开局。',

    'level.beginner': '入门',
    'level.easy': '简单',
    'level.medium': '中等',
    'level.hard': '困难',
    'level.master': '大师',

    'hint.fromBook': '提示：走 {move}（来自开局库）',
    'hint.fromSearch': '提示：走 {move}（来自深度 {depth} 的搜索）',

    // Failures, keyed by the codes the server sends. The server's own English
    // message is never shown, for the same reason it is not shown in English.
    'error.bad_move': '该着法在此局面不合法。',
    'error.bad_square': '该位置不在棋盘上。',
    'error.bad_level': '本游戏没有该难度。',
    'error.bad_player_side': '你只能执红方或黑方。',
    'error.bad_body': '请求格式不正确。',
    'error.not_your_turn': '还没轮到你走。',
    'error.not_ai_turn': '还没轮到电脑走。',
    'error.game_over': '对局已结束。请开始新对局以继续。',
    'error.no_undo': '没有可悔的棋。',
    'error.ai_busy': '电脑正在思考。',
    'error.stale': '电脑思考期间对局已改变。请重试。',
    'error.offline': '开局库不可用。',
    'error.no_move': '没有合法的着法可走。',
    'error.internal': '服务器出错了。',
    'error.network': '服务器没有响应。',
    'error.unknown': '出错了。',
  },
});
