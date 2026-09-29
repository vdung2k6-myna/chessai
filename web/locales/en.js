// en.js — the English interface strings.
//
// This table is also the fallback: a key another locale does not translate shows
// the text here rather than a hole. It is therefore the one table that must be
// complete, and the key-set test in testdata/ holds every locale to its key set.
//
// Two kinds of template, matching the two ways app.js inserts them:
//
//   * set as text with t()    — {placeholders} are inserted verbatim
//   * set as HTML with tHTML() — {placeholders} are escaped, the markup is not
//
// Markup in a template is how a sentence keeps its bold or its link in every
// language. Only templates are allowed to carry it; values never are.
globalThis.XQ_LOCALES = Object.assign(globalThis.XQ_LOCALES || {}, {
  en: {
    // The game's name. Not the board's: the pieces and the river keep their
    // Chinese characters in every locale, because that is board notation rather
    // than interface language.
    'app.name': 'Xiangqi',
    'app.tagline': 'Playing against the <strong>godogpaw</strong> engine, with <a href="https://www.chessdb.cn/cloudbook_api_en.html" target="_blank" rel="noreferrer">chessdb.cn</a> as its opening book.',

    'board.label': 'Xiangqi board',

    'status.loading': 'Loading…',
    'status.yourTurn': '{side} to move — your turn',
    'status.yourTurnCheck': '{side} to move — your turn (you are in check)',
    'status.computerTurn': "{side} to move — the computer's turn",
    'status.thinking': 'The computer is thinking…',
    'status.draw': 'Draw — {reason}',
    'status.winYou': '{winner} wins by {reason} — you win',
    'status.winComputer': '{winner} wins by {reason} — the computer wins',

    // The sides. These name the players, not the pieces on the board.
    'side.red': 'Red',
    'side.black': 'Black',

    // How a game ended, keyed by the reason code the server sends. The winner is
    // a separate field, which is why one code covers both directions of a
    // perpetual check: the sentence supplies whose win it is.
    'reason.checkmate': 'checkmate',
    'reason.stalemate': 'stalemate',
    'reason.perpetual_check': 'a perpetual check or chase',
    'reason.repetition': 'threefold repetition',
    'reason.sixty_move': 'the sixty-move rule',
    'reason.unknown': 'an unknown reason',

    // The computer's last move. The move is escaped into the markup by tHTML;
    // the badge and the statistics are appended as elements, so no locale has to
    // decide where they go.
    'aiMove.played': 'The computer played <strong>{move}</strong>',
    'aiMove.detail': ' — {stats}',
    'badge.book': 'book',
    'badge.search': 'search',
    'badge.fallback': 'fallback',
    'stat.depth': 'depth {n}',
    'stat.score': 'score {n}',
    'stat.nodes': '{n} nodes',
    'stat.time': '{n} ms',
    'list.separator': ', ',

    // Whose numbers the sidebar is showing. chessdb reports every score from the
    // point of view of the side to move, which is only red half the time; a
    // "+1873" with black winning would otherwise read as red winning.
    'pov.red': "Scores and win rates are from Red's point of view.",
    'pov.black': "Scores and win rates are from Black's point of view.",

    'analysis.heading': 'Analysis',
    'analysis.cloudBest': 'Cloud best',
    'analysis.cloudEval': 'Cloud eval',
    'analysis.cloudDepth': 'Cloud depth',
    'analysis.topMoves': 'Top book moves',
    'analysis.pv': 'Principal variation',
    'analysis.plies': '{n} plies',
    'analysis.notInBook': 'This position is not in the cloudbook.',
    'analysis.partial': 'Some cloudbook queries failed; this view may be incomplete.',
    'analysis.unavailable': 'Cloudbook unavailable: {error}',

    'table.move': 'Move',
    'table.score': 'Score',
    // A non-breaking space, so the header cannot wrap between "Win" and "%".
    'table.winrate': 'Win %',
    'table.note': 'Note',

    'button.newGame': 'New game',
    'button.undo': 'Undo',
    'button.hint': 'Hint',

    // The picker's own label. Its options are not translated: each is written in
    // the language it selects, so a player who has landed in a language they
    // cannot read can still find their way out.
    'settings.language': 'Language',
    'settings.playAs': 'Play as',
    'settings.sideRed': 'Red (first)',
    'settings.sideBlack': 'Black (second)',
    'settings.level': 'Level',
    'settings.useBook': 'Use the opening book',
    'settings.bookNote': 'In the opening the book supplies the moves the computer chooses among, at every level, so even the easiest level opens with moves a player would recognize.',

    // Difficulty levels, keyed by their names — the same names the server knows
    // them by and the same values the picker sends back, so a level and its
    // label cannot drift apart.
    'level.beginner': 'Beginner',
    'level.easy': 'Easy',
    'level.medium': 'Medium',
    'level.hard': 'Hard',
    'level.master': 'Master',

    'hint.fromBook': 'Hint: play {move} (from the opening book)',
    'hint.fromSearch': 'Hint: play {move} (from a depth-{depth} search)',

    // Failures, keyed by the codes the server sends. The server also sends an
    // English message with each one, and the interface deliberately ignores it:
    // the message is for the server log, and showing it would put English in
    // front of a player who reads another language.
    //
    // A code not listed here gets 'error.unknown', so an unrecognized or newly
    // added code reads as a plain apology rather than as a bare identifier.
    'error.bad_move': 'That move is not legal here.',
    'error.bad_square': 'That square is not on the board.',
    'error.bad_level': 'That is not a difficulty level this game plays.',
    'error.bad_player_side': 'You can play as red or black, and nothing else.',
    'error.bad_body': 'That request was malformed.',
    'error.not_your_turn': 'It is not your turn.',
    'error.not_ai_turn': "It is not the computer's turn.",
    'error.game_over': 'The game is over. Start a new one to keep playing.',
    'error.no_undo': 'There is nothing to undo.',
    'error.ai_busy': 'The computer is already thinking.',
    'error.stale': 'The game changed while the computer was thinking. Try again.',
    'error.offline': 'The opening book is unavailable.',
    'error.no_move': 'There is no legal move to play.',
    'error.internal': 'Something went wrong on the server.',
    'error.network': 'The server is not responding.',
    'error.unknown': 'Something went wrong.',
  },
});
