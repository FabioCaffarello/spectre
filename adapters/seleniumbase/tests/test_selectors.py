"""Unit tests for the SELECTOR_KIND_TEXT XPath escape helper.

ADR-0015 §3 records the decision and the rejected alternatives.
"""

from __future__ import annotations

from spectre_seleniumbase.selectors import text_selector_to_xpath


def test_no_quotes_uses_single_quoted_form() -> None:
    assert text_selector_to_xpath("hello") == "//*[contains(text(), 'hello')]"


def test_only_single_quotes_uses_double_quoted_form() -> None:
    assert text_selector_to_xpath("it's") == '//*[contains(text(), "it\'s")]'


def test_only_double_quotes_uses_single_quoted_form() -> None:
    assert text_selector_to_xpath('say "hi"') == "//*[contains(text(), 'say \"hi\"')]"


def test_both_quote_types_use_concat() -> None:
    """The dual-quote case splices a literal `\"` between
    single-quoted segments via XPath ``concat()``."""
    out = text_selector_to_xpath('she said "it\'s me"')
    assert out.startswith("//*[contains(text(), concat(")
    assert out.endswith("))]")
    # The concat body should contain the two segments and the
    # literal double-quote separator.
    body = out[len("//*[contains(text(), concat(") : -len("))]")]
    assert body == "'she said '" + ", '\"', " + '"it\'s me"' + ", '\"', " + "''"


def test_empty_string_round_trips_to_no_quote_form() -> None:
    assert text_selector_to_xpath("") == "//*[contains(text(), '')]"
