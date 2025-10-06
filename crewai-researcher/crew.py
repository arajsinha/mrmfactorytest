import sys
import json
import os
from crewai import Agent, Task, Crew, Process
from crewai_tools import SerperDevTool

# Get the research topic from the command-line argument
topic = sys.argv[1]

# Initialize the search tool
search_tool = SerperDevTool(api_key=os.environ.get("SERPER_API_KEY"))

# Define the Researcher Agent
researcher = Agent(
  role='Senior Research Analyst',
  goal=f'Uncover groundbreaking technologies and trends about {topic}',
  backstory="You're a renowned research analyst, known for your ability to find signal in the noise.",
  tools=[search_tool],
  allow_delegation=False
)

# Define the Writer Agent
writer = Agent(
  role='Senior Technology Writer',
  goal=f'Craft a compelling and informative blog post about {topic}',
  backstory="You're a famous technology writer, capable of simplifying complex topics for a broad audience.",
  allow_delegation=False
)

# Define the Tasks
research_task = Task(
  description=f'Conduct a comprehensive analysis of the latest trends in {topic}. Identify key players, innovations, and market forecasts.',
  expected_output='A detailed report summarizing your findings.',
  agent=researcher
)

write_task = Task(
  description='Using the research report, write an engaging blog post. It should be easy to understand, well-structured, and highlight the most important findings.',
  expected_output=f'A 500-word blog post about {topic}, formatted in markdown.',
  agent=writer
)

# Form the Crew with a sequential process
crew = Crew(
  agents=[researcher, writer],
  tasks=[research_task, write_task],
  process=Process.sequential
)

# Execute the crew's work
result = crew.kickoff()

# Print the final result as a JSON object to standard output
print(json.dumps({"result": result}))