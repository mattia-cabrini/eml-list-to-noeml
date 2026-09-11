# General Instruction

1. Do not run commands, unless unavoidable; Read and write files directly, instead.
2. Keep the code human readable.
3. Put the copyright notice on each files, with SPDX License Identifier compatible with LICENSE.
4. Read CLAUDE.md at each run.
5. Do not commit.

# Orders

## IMPLEMENT

When the user prompt states IMPLEMENT, you shall read and implement the specifications stored into {workingDirectory}/ignore/IMPLEMENT.md.

## MAKE-POLISH

When the user prompt states MAKE-POLISH, you shall run through the code and:

1. Deduplicate code whenever possible;
2. Remove any unnecessary complexity;
3. Comment the code in order for the user to gain some useful insight, when reading the code.

## SUMMARIZE

When the user prompt states SUMMARIZE, you shall run through the staged diff and write a full commit message in {workingDirectory}/ignore/commit_message.txt.
