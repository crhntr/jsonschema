# Surveys

Write a survey, send people a link, and read the results. Both forms are
rendered by [`jsonhandler`](../../jsonhandler/README.md), but only one of the
schemas behind them is written by hand.

```sh
go run . -data ./data      # then open http://localhost:8080/
```

Try it with the sample: copy `testdata/docs-feedback.json` to
`data/surveys/docs-feedback.json`, or write a survey from scratch at
`/surveys/new`.

## How it works

- **The author's form** comes from `schema/definition.json`: a title, a
  description, and a list of questions, each a `oneOf` over four kinds (text,
  multiple choice, scale, yes or no) that tell themselves apart by a constant
  `kind`. In the form, each question is a variant picker with that kind's
  fields.
- **The respondent's form** comes from a JSON Schema that is *built from the
  survey* (`internal/survey`): one property per question, with `enum` and
  `x-form` radio buttons for choices, a checkbox group with `minItems: 1` for
  a required multiple choice, and so on. The schema is the survey's contract:
  every response stored validates against it.
- **Respondents can fill the form in one question at a time.** The wizard is
  already part of `jsonhandler`; here it only needs `NoJSON`, because a public
  form has no use for raw JSON. Next cannot skip a required question: the
  browser enforces it for radio buttons, and the server enforces it for the
  rest, such as a required checkbox group.
- **Results** are tallied from the stored responses by the survey's own
  question list (`Definition.Summarize`), with a `<meter>` per answer.
- **Storage** is a directory of files: `surveys/<name>.json` for each survey
  and `responses/<name>.jsonl` with one response per line. Names are checked
  before they become file names.

Because surveys are data, the server keeps a small cache of the form handler
for each survey, keyed by the hash of its definition, so a busy survey does
not rebuild its schema for every respondent.

## What it asks of jsonhandler

Building this app drove several changes to the library: `NoJSON` and
`NoWizard`, labelling the blank "No answer" radio of an optional question,
leaving an untouched required checkbox group alone until someone submits,
showing every kind of problem in a submission at once, and making `New` cheap
enough to call per request.

## Limitations

- There are no accounts. Anyone can write, read results of, and delete a
  survey; put it behind your own authentication before sharing it.
- Answers are stored under a short form of the question's prompt, so
  rewording a question starts its answers afresh.
- A response is anonymous and nothing stops one person answering twice.
